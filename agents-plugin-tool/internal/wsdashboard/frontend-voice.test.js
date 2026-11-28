const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const assetsDir = path.join(__dirname, 'assets');
const voicePttJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice-ptt.js'), 'utf8');
const voiceJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice.js'), 'utf8');
const voiceNativeJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice-native.js'), 'utf8');
const deliveryJS = fs.readFileSync(path.join(assetsDir, 'dashboard-composer-delivery.js'), 'utf8');

class FakeElement {
  constructor(tag, id, cls) {
    this.nodeType = 1;
    this.tagName = String(tag || 'div').toUpperCase();
    this.id = id || '';
    this.className = cls || '';
    this.attrs = new Map();
    this.listeners = new Map();
    this.children = [];
    this.parentNode = null;
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.title = '';
    this.type = '';
    this._text = '';
    this.value = '';
    this.selectionStart = 0;
    this.selectionEnd = 0;
    this.focused = false;
    this.rect = { left: 100, top: 10, width: 400, height: 60 };
  }
  get childNodes() { return this.children; }
  get textContent() { return this._text; }
  set textContent(v) {
    this._text = String(v == null ? '' : v);
    if (this._text === '') this.children = [];
  }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  hasAttribute(k) { return this.attrs.has(k); }
  removeAttribute(k) { this.attrs.delete(k); }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  insertBefore(child, ref) {
    child.parentNode = this;
    const idx = ref ? this.children.indexOf(ref) : -1;
    if (idx >= 0) this.children.splice(idx, 0, child);
    else this.children.push(child);
    return child;
  }
  removeChild(child) {
    const idx = this.children.indexOf(child);
    if (idx >= 0) this.children.splice(idx, 1);
    child.parentNode = null;
    return child;
  }
  remove() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }
  contains(node) {
    if (node === this) return true;
    for (const child of this.children) {
      if (child === node || (typeof child.contains === 'function' && child.contains(node))) return true;
    }
    return false;
  }
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }
  removeEventListener(type, fn) {
    const arr = this.listeners.get(type);
    if (!arr) return;
    const idx = arr.indexOf(fn);
    if (idx >= 0) arr.splice(idx, 1);
  }
  dispatchEvent(ev) {
    const type = typeof ev === 'string' ? ev : ev.type;
    const arr = (this.listeners.get(type) || []).slice();
    for (const fn of arr) fn(ev);
    return true;
  }
  click() {
    this.dispatchEvent({ type: 'click', preventDefault() {} });
  }
  focus() { this.focused = true; }
  getBoundingClientRect() { return this.rect; }
  setRangeText(replacement, start, end, selectMode) {
    const a = typeof start === 'number' ? start : this.selectionStart;
    const b = typeof end === 'number' ? end : this.selectionEnd;
    this.value = this.value.slice(0, a) + replacement + this.value.slice(b);
    if (selectMode === 'end') {
      this.selectionStart = this.selectionEnd = a + replacement.length;
    }
    this.lastSetRangeText = { replacement, start: a, end: b, selectMode };
  }
  setSelectionRange(a, b) {
    this.selectionStart = a;
    this.selectionEnd = b;
  }
  querySelector(sel) {
    const matches = (el) => {
      if (sel.startsWith('.')) {
        const cls = sel.slice(1);
        return el.className.split(/\s+/).includes(cls);
      }
      const attrMatch = sel.match(/^\[([^=\]]+)="([^"]*)"\]$/);
      if (attrMatch) return el.getAttribute(attrMatch[1]) === attrMatch[2];
      const attrPresence = sel.match(/^\[([^=\]]+)\]$/);
      if (attrPresence) return el.hasAttribute(attrPresence[1]);
      return false;
    };
    const walk = (node) => {
      for (const child of node.children) {
        if (matches(child)) return child;
        const nested = walk(child);
        if (nested) return nested;
      }
      return null;
    };
    return walk(this);
  }
}

class FakeFormData {
  constructor() { this.entries = []; }
  append(name, value, filename) { this.entries.push({ name, value, filename }); }
  get(name) { const hit = this.entries.find(e => e.name === name); return hit ? hit.value : null; }
  getEntry(name) { return this.entries.find(e => e.name === name) || null; }
}

class FakeEvent {
  constructor(type, init) { this.type = type; Object.assign(this, init || {}); }
}

function createEnv(sharedTop) {
  const root = new FakeElement('html');
  const composer = new FakeElement('div', 'composer', 'composer');
  const field = new FakeElement('div', '', 'cfield');
  const ctext = new FakeElement('textarea', 'ctext');
  const editor = new FakeElement('div', '', 'wsc-editor');
  editor.setAttribute('contenteditable', 'true');
  ctext.wsc = {
    editor,
    anchor(pos) { return { x: 140 + pos * 4, top: 18, bottom: 36 }; }
  };
  const cap = new FakeElement('div', 'cvox-cap', 'cvox-cap');
  cap.hidden = true;
  field.appendChild(ctext);
  field.appendChild(cap);
  const btn = new FakeElement('button', 'cvox', 'cvox');
  btn.hidden = true;
  const csend = new FakeElement('button', 'csend', 'csend');
  composer.appendChild(field);
  composer.appendChild(btn);
  composer.appendChild(csend);

  const docListeners = new Map();
  const winListeners = new Map();
  const doc = {
    hidden: false,
    documentElement: root,
    getElementById(id) {
      if (id === 'composer') return composer;
      if (id === 'ctext') return ctext;
      if (id === 'csend') return csend;
      if (id === 'cvox') return btn;
      if (id === 'cvox-cap') return cap;
      return null;
    },
    createElement(tag) { return new FakeElement(tag); },
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

  const windowObj = {
    document: doc,
    navigator: {},
    wsT: (k) => k,
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
    }
  };
  windowObj.top = sharedTop || windowObj;

  const ctx = {
    window: windowObj,
    document: doc,
    navigator: windowObj.navigator,
    Blob,
    FormData: FakeFormData,
    Event: FakeEvent,
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
  // Prevent auto-attach before test configures mocks by returning null during initial script eval.
  const origGetById = doc.getElementById;
  doc.getElementById = () => null;
  windowObj.__wsVoiceNativeNoAuto = true;
  vm.runInContext(voicePttJS, ctx);
  vm.runInContext(voiceJS, ctx);
  vm.runInContext(voiceNativeJS, ctx);
  doc.getElementById = origGetById;

  return { ctx, window: windowObj, doc, root, composer, field, ctext, editor, cap, btn, csend };
}

function makeFakeMedia(supportedMime = 'audio/webm;codecs=opus') {
  const tracks = [];
  let gumCalls = 0;
  let lastRecorder = null;
  const mediaDevices = {
    getUserMedia(constraints) {
      gumCalls++;
      const track = { stopped: false, stop() { this.stopped = true; } };
      tracks.push(track);
      return Promise.resolve({ constraints, getTracks() { return [track]; } });
    }
  };
  class FakeMediaRecorder {
    static isTypeSupported(mime) { return mime === supportedMime; }
    constructor(stream, opts) {
      this.stream = stream;
      this.mimeType = (opts && opts.mimeType) || supportedMime;
      this.state = 'inactive';
      lastRecorder = this;
    }
    start() { this.state = 'recording'; }
    stop() {
      this.state = 'inactive';
      if (this.ondataavailable) {
        this.ondataavailable({ data: new Blob([new Uint8Array([1, 2, 3, 4])], { type: this.mimeType }) });
      }
      if (this.onstop) this.onstop();
    }
  }
  return {
    mediaDevices,
    MediaRecorder: FakeMediaRecorder,
    get gumCalls() { return gumCalls; },
    get tracks() { return tracks; },
    get lastRecorder() { return lastRecorder; }
  };
}

function makeSSEResponse(chunks) {
  const enc = new TextEncoder();
  let idx = 0;
  return {
    ok: true,
    status: 200,
    headers: { get(k) { return k.toLowerCase() === 'content-type' ? 'text/event-stream' : ''; } },
    body: {
      getReader() {
        return {
          read() {
            if (idx >= chunks.length) return Promise.resolve({ done: true, value: undefined });
            const value = enc.encode(chunks[idx++]);
            return Promise.resolve({ done: false, value });
          },
          cancel() { idx = chunks.length; return Promise.resolve(); }
        };
      }
    }
  };
}

const flush = () => new Promise(r => setImmediate(r));

test('mic button stays hidden when ineligible, shows unconfigured when eligible without usable backend, and activates once usable', async () => {
  const env = createEnv();
  let capOk = false;
  let capResponse = { backends: [], selected: null };
  const fakeFetch = async (url) => {
    assert.equal(url, '/api/voice/capabilities');
    return { ok: capOk, json: async () => capResponse };
  };

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    autoDiscover: false
  });

  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, true, 'hidden when capabilities request is rejected (non-local / 404)');

  capOk = true;
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, false, 'visible when eligible even with no backends');
  assert.equal(env.btn.getAttribute('data-state'), 'unconfigured');

  capResponse = {
    backends: [{ id: 'openai_api', kind: 'external', label: 'OpenAI Platform', available: false }],
    selected: null
  };
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, false, 'visible as unconfigured when selected backend is unavailable');
  assert.equal(env.btn.getAttribute('data-state'), 'unconfigured');

  capResponse = {
    backends: [{ id: 'openai_api', kind: 'external', label: 'OpenAI Platform', available: false }],
    selected: { backend: 'openai_api', model: 'gpt-transcribe' }
  };
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, false, 'visible as unconfigured when selected object names an unavailable backend');
  assert.equal(env.btn.getAttribute('data-state'), 'unconfigured');

  capResponse = {
    backends: [{ id: 'openai_api', kind: 'external', label: 'OpenAI Platform', available: true, models: [{ id: 'gpt-transcribe', streaming: true, language_field: 'languages' }] }],
    selected: { backend: 'openai_api', model: 'gpt-transcribe', language: 'en' }
  };
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, false, 'visible once selected object names a usable backend');
  assert.equal(env.btn.getAttribute('data-state'), null, 'clears unconfigured state once usable');
  handle.destroy();
});

test('common WsVoiceInput interface accepts pluggable native backends identically to external backends', async () => {
  const env = createEnv();
  env.ctext.value = 'hi !';
  env.ctext.selectionStart = 3;
  env.ctext.selectionEnd = 3;

  let emitPartial = null;
  let started = false;
  env.window.WsVoiceInput.registerBackend({
    id: 'apple-speech',
    label: 'Apple Speech',
    kind: 'native',
    available: () => true,
    onPartial: (cb) => { emitPartial = cb; },
    start: () => { started = true; return Promise.resolve(); },
    stop: () => Promise.resolve({ text: 'world' }),
    cancel: () => {}
  });

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    autoDiscover: false
  });
  handle.controller.setCapabilities({ selected: 'apple-speech', backends: [] });
  assert.equal(env.btn.hidden, false);

  env.btn.click();
  assert.equal(started, true);
  assert.equal(env.composer.getAttribute('data-voice-locked'), '1');
  emitPartial('wor');
  const liveSpan = env.editor.querySelector('[data-voice-span]');
  assert.ok(liveSpan, 'inline [data-voice-span] rendered inside WsCapsules editor');
  assert.equal(liveSpan.textContent, 'wor');
  assert.equal(env.ctext.value, 'hi !', 'interim partial must not touch composer text');

  env.btn.click();
  await flush();
  assert.equal(env.ctext.value, 'hi world!');
  assert.equal(env.composer.hasAttribute('data-voice-locked'), false);
  assert.equal(env.editor.querySelector('[data-voice-span]'), null);
  assert.equal(env.cap.hidden, true);
  handle.destroy();
});

test('push-to-talk calls getUserMedia synchronously, streams SSE preview in inline span, and inserts final text via setRangeText', async () => {
  const env = createEnv();
  const media = makeFakeMedia('audio/webm;codecs=opus');
  env.ctext.value = 'prefix suffix';
  env.ctext.selectionStart = 7;
  env.ctext.selectionEnd = 7;

  let inputEvents = 0;
  env.ctext.addEventListener('input', () => { inputEvents++; });

  let resolveSecondChunk;
  const secondChunkGate = new Promise(r => { resolveSecondChunk = r; });
  let uploadCount = 0;
  let lastForm = null;

  const fakeFetch = async (url, init) => {
    if (url === '/api/voice/capabilities') {
      return {
        ok: true,
        json: async () => ({
          backends: [{ id: 'openai_api', kind: 'external', label: 'OpenAI Platform', available: true, models: [{ id: 'gpt-4o-mini-transcribe', streaming: true, language_field: 'language' }] }],
          selected: { backend: 'openai_api', model: 'gpt-4o-mini-transcribe', language: 'en' }
        })
      };
    }
    assert.equal(url, '/api/voice/transcribe');
    uploadCount++;
    lastForm = init.body;
    const enc = new TextEncoder();
    const frames = [
      'event: transcript.text.delta\ndata: {"type":"transcript.text.delta","delta":"hello "}\n\n',
      'event: transcript.text.delta\ndata: {"type":"transcript.text.delta","delta":"voice"}\n\n',
      'data: [DONE]\n\n',
      'event: transcript.text.done\ndata: {"type":"transcript.text.done","text":"hello voice!"}\n\n'
    ];
    let i = 0;
    return {
      ok: true,
      status: 200,
      headers: { get(k) { return k.toLowerCase() === 'content-type' ? 'text/event-stream' : ''; } },
      body: {
        getReader() {
          return {
            async read() {
              if (i === 1) await secondChunkGate;
              if (i >= frames.length) return { done: true, value: undefined };
              return { done: false, value: enc.encode(frames[i++]) };
            },
            cancel() { i = frames.length; return Promise.resolve(); }
          };
        }
      }
    };
  };

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    mediaDevices: media.mediaDevices,
    MediaRecorder: media.MediaRecorder,
    autoDiscover: false
  });
  await handle.controller.refreshCapabilities();

  // Synchronous getUserMedia inside click gesture (no awaits before click returns).
  env.btn.click();
  assert.equal(media.gumCalls, 1, 'getUserMedia must fire synchronously inside click gesture');
  assert.equal(env.composer.getAttribute('data-voice-locked'), '1');
  assert.equal(env.ctext.disabled, true);
  assert.equal(env.csend.disabled, true);
  assert.equal(env.editor.getAttribute('contenteditable'), 'false');
  const recSpan = env.editor.querySelector('[data-voice-span]');
  assert.ok(recSpan, 'inline span inserted inside editor during recording');
  assert.equal(recSpan.getAttribute('data-voice-span'), 'listening');
  assert.equal(env.cap.hidden, true);

  await flush();
  // Stop recording -> starts upload & SSE stream.
  env.btn.click();
  await flush();
  await flush();

  assert.equal(uploadCount, 1);
  assert.equal(lastForm.getEntry('audio').filename, 'recording.webm');
  assert.equal(lastForm.get('backend'), 'openai_api');
  assert.equal(lastForm.get('model'), 'gpt-4o-mini-transcribe');
  assert.equal(lastForm.get('language'), 'en');
  assert.equal(media.tracks[0].stopped, true, 'microphone tracks released on stop');

  // While streaming, partial text appears only inside inline span; composer stays untouched.
  assert.equal(env.editor.querySelector('[data-voice-span]').getAttribute('data-voice-span'), 'refining');
  assert.equal(env.editor.querySelector('[data-voice-span]').textContent, 'hello ');
  assert.equal(env.ctext.value, 'prefix suffix');
  assert.equal(env.composer.getAttribute('data-voice-locked'), '1');

  // Finish stream.
  resolveSecondChunk();
  await flush();
  await flush();
  await flush();

  // The commit swallows the typed space before the caret and re-adds exactly one.
  assert.equal(env.ctext.lastSetRangeText.replacement, ' hello voice!');
  assert.equal(env.ctext.lastSetRangeText.start, 6);
  assert.equal(env.ctext.lastSetRangeText.end, 7);
  assert.equal(env.ctext.value, 'prefix hello voice!suffix');
  assert.equal(inputEvents, 1);
  assert.equal(env.composer.hasAttribute('data-voice-locked'), false);
  assert.equal(env.ctext.disabled, false);
  assert.equal(env.csend.disabled, false);
  assert.equal(env.editor.getAttribute('contenteditable'), 'true');
  assert.equal(env.cap.hidden, true);
  handle.destroy();
});

test('non-streaming JSON response inserts final text at captured caret', async () => {
  const env = createEnv();
  const media = makeFakeMedia('audio/mp4');
  env.ctext.value = 'abc';
  env.ctext.selectionStart = 1;
  env.ctext.selectionEnd = 2;

  const fakeFetch = async (url, init) => {
    if (url === '/api/voice/capabilities') {
      return {
        ok: true,
        json: async () => ({
          backends: [{ id: 'groq', kind: 'external', available: true, models: [{ id: 'whisper-large-v3-turbo', streaming: false, language_field: 'language' }] }],
          selected: { backend: 'groq', model: 'whisper-large-v3-turbo' }
        })
      };
    }
    assert.equal(init.body.getEntry('audio').filename, 'recording.m4a');
    return {
      ok: true,
      status: 200,
      headers: { get() { return 'application/json'; } },
      json: async () => ({ text: 'XYZ' })
    };
  };

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    mediaDevices: media.mediaDevices,
    MediaRecorder: media.MediaRecorder,
    autoDiscover: false
  });
  await handle.controller.refreshCapabilities();

  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();

  assert.equal(env.ctext.value, 'a XYZc', 'dictation after text on the line gets exactly one separating space');
  assert.equal(env.composer.hasAttribute('data-voice-locked'), false);
  handle.destroy();
});

test('memory-only last recording supports resend on failure and discards on new recording, unload, or page hide', async () => {
  const env = createEnv();
  const media = makeFakeMedia('audio/webm;codecs=opus');
  env.ctext.value = '';

  let attempt = 0;
  const fakeFetch = async (url) => {
    if (url === '/api/voice/capabilities') {
      return {
        ok: true,
        json: async () => ({
          backends: [{ id: 'openai_api', kind: 'external', available: true, models: [{ id: 'gpt-4o-mini-transcribe', streaming: true, language_field: 'language' }] }],
          selected: { backend: 'openai_api', model: 'gpt-4o-mini-transcribe' }
        })
      };
    }
    attempt++;
    if (attempt === 1) {
      return {
        ok: false,
        status: 504,
        headers: { get() { return 'application/json'; } },
        json: async () => ({ code: 'upstream_timeout', message: 'timeout' })
      };
    }
    return makeSSEResponse([
      'event: transcript.text.done\ndata: {"type":"transcript.text.done","text":"retried ok"}\n\n'
    ]);
  };

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    mediaDevices: media.mediaDevices,
    MediaRecorder: media.MediaRecorder,
    autoDiscover: false
  });
  await handle.controller.refreshCapabilities();

  // First attempt fails -> composer unlocks, Resend button appears, audio kept in memory.
  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();

  assert.equal(env.composer.hasAttribute('data-voice-locked'), false, 'composer unlocks on provider error');
  assert.equal(env.cap.getAttribute('data-state'), 'error');
  assert.equal(env.cap.querySelector('.cvox-text').textContent, 'js.voice.err.timeout');
  assert.equal(handle.controller.canRetry(), true);
  const resendBtn = env.cap.querySelector('[data-voice-action="resend"]');
  assert.ok(resendBtn, 'resend button rendered');

  // Resend re-uploads without invoking getUserMedia again.
  resendBtn.click();
  await flush();
  await flush();
  await flush();
  assert.equal(media.gumCalls, 1, 'resend must not re-record audio');
  assert.equal(env.ctext.value, 'retried ok');
  assert.equal(handle.controller.canRetry(), false, 'last audio discarded after successful resend');

  // Now test visibility hide (data-animation-paused) cancels active recording, releases mic, drops lock & last audio.
  env.btn.click();
  await flush();
  assert.equal(env.composer.getAttribute('data-voice-locked'), '1');
  assert.equal(media.tracks[1].stopped, false);

  env.root.setAttribute('data-animation-paused', '');
  env.doc.dispatchEvent({ type: 'ws-page-visibility' });
  assert.equal(media.tracks[1].stopped, true, 'page hide/tray must stop mic track immediately');
  assert.equal(env.composer.hasAttribute('data-voice-locked'), false);
  assert.equal(env.cap.hidden, true);
  assert.equal(handle.controller.canRetry(), false);

  // Also test discard on pagehide after an error.
  env.root.removeAttribute('data-animation-paused');
  attempt = 0; // next upload will fail again
  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();
  assert.equal(handle.controller.canRetry(), true);
  env.window.dispatchEvent({ type: 'pagehide' });
  assert.equal(handle.controller.canRetry(), false, 'pagehide discards in-memory last recording');
  assert.equal(env.cap.hidden, true);
  handle.destroy();
});

test('single-recorder guard refuses concurrent recording across composers sharing window.top', async () => {
  const sharedTop = { document: {} };
  const envA = createEnv(sharedTop);
  const envB = createEnv(sharedTop);
  const mediaA = makeFakeMedia();
  const mediaB = makeFakeMedia();

  const capsFetch = async () => ({
    ok: true,
    json: async () => ({
      backends: [{ id: 'openai_api', kind: 'external', available: true, models: [{ id: 'gpt-4o-mini-transcribe', streaming: true, language_field: 'language' }] }],
      selected: { backend: 'openai_api', model: 'gpt-4o-mini-transcribe' }
    })
  });

  const handleA = envA.window.WsVoiceInput.attachComposer({
    document: envA.doc, fetch: capsFetch, mediaDevices: mediaA.mediaDevices, MediaRecorder: mediaA.MediaRecorder, autoDiscover: false
  });
  const handleB = envB.window.WsVoiceInput.attachComposer({
    document: envB.doc, fetch: capsFetch, mediaDevices: mediaB.mediaDevices, MediaRecorder: mediaB.MediaRecorder, autoDiscover: false
  });
  await handleA.controller.refreshCapabilities();
  await handleB.controller.refreshCapabilities();

  // Start A.
  envA.btn.click();
  await flush();
  assert.equal(mediaA.gumCalls, 1);
  assert.equal(envA.window.WsVoiceInput.hasActiveRecorderLock(), true);

  // Attempt B while A holds the lock -> refused with recorder_busy without calling getUserMedia.
  envB.btn.click();
  await flush();
  assert.equal(mediaB.gumCalls, 0, 'second composer must not open mic while first holds lock');
  assert.equal(envB.cap.getAttribute('data-state'), 'error');
  assert.equal(envB.cap.querySelector('.cvox-text').textContent, 'js.voice.err.busy');

  // Cancel A -> lock released -> B can start.
  handleA.controller.cancel({ discardLast: true });
  assert.equal(envA.window.WsVoiceInput.hasActiveRecorderLock(), false);
  envB.btn.click();
  await flush();
  assert.equal(mediaB.gumCalls, 1);

  handleA.destroy();
  handleB.destroy();
});

test('codec negotiation, PCM16 WAV fallback encoding, and delivery lock guard', async () => {
  const env = createEnv();
  const api = env.window.WsVoiceInput;

  assert.deepEqual(
    { ...api.selectRecorderCodec({ isTypeSupported: (m) => m === 'audio/ogg;codecs=opus' }) },
    { mode: 'recorder', mime: 'audio/ogg;codecs=opus', ext: 'ogg' }
  );
  assert.deepEqual(
    { ...api.selectRecorderCodec({ isTypeSupported: () => false }) },
    { mode: 'pcm', mime: 'audio/wav', ext: 'wav' }
  );
  assert.deepEqual(
    { ...api.selectRecorderCodec(null) },
    { mode: 'pcm', mime: 'audio/wav', ext: 'wav' }
  );

  const wavBlob = api.encodeWavPCM16([new Float32Array([0, 0.5, -0.5, 1.0])], 16000);
  assert.equal(wavBlob.type, 'audio/wav');
  const buf = new Uint8Array(await wavBlob.arrayBuffer());
  assert.equal(buf.length, 44 + 4 * 2);
  assert.equal(String.fromCharCode(buf[0], buf[1], buf[2], buf[3]), 'RIFF');
  assert.equal(String.fromCharCode(buf[8], buf[9], buf[10], buf[11]), 'WAVE');

  // End-to-end PCM16 fallback upload when MediaRecorder is unavailable.
  const media = makeFakeMedia();
  let uploadedFilename = '';
  const fakeFetch = async (url, init) => {
    if (url === '/api/voice/capabilities') {
      return {
        ok: true,
        json: async () => ({
          backends: [{ id: 'local/whisper', kind: 'external', available: true, models: [{ id: 'whisper-1', streaming: false, language_field: 'language' }] }],
          selected: { backend: 'local/whisper', model: 'whisper-1' }
        })
      };
    }
    uploadedFilename = init.body.getEntry('audio').filename;
    return { ok: true, status: 200, headers: { get() { return 'application/json'; } }, json: async () => ({ text: 'wav ok' }) };
  };
  class FakeAudioContext {
    constructor() { this.sampleRate = 16000; this.destination = {}; }
    createMediaStreamSource() { return { connect() {}, disconnect() {} }; }
    createScriptProcessor() {
      const proc = {
        connect() {
          if (proc.onaudioprocess) {
            proc.onaudioprocess({ inputBuffer: { getChannelData() { return new Float32Array([0.1, -0.1]); } } });
          }
        },
        disconnect() {}
      };
      return proc;
    }
    close() {}
  }

  const handle = api.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    mediaDevices: media.mediaDevices,
    MediaRecorder: null,
    AudioContext: FakeAudioContext,
    autoDiscover: false
  });
  await handle.controller.refreshCapabilities();
  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();
  assert.equal(uploadedFilename, 'recording.wav');
  assert.equal(env.ctext.value, 'wav ok');
  handle.destroy();

  // Verify dashboard-composer-delivery.js guards sendMessage while data-voice-locked.
  assert.match(deliveryJS, /data-voice-locked/);
});

test('incomplete SSE stream without transcript.text.done never commits partial text and preserves resend audio', async () => {
  const env = createEnv();
  const media = makeFakeMedia('audio/webm;codecs=opus');
  env.ctext.value = 'keep me';
  env.ctext.selectionStart = 7;
  env.ctext.selectionEnd = 7;

  let attempts = 0;
  const fakeFetch = async (url) => {
    if (url === '/api/voice/capabilities') {
      return {
        ok: true,
        json: async () => ({
          backends: [{ id: 'openai_api', kind: 'external', available: true, models: [{ id: 'gpt-transcribe', streaming: true, language_field: 'languages' }] }],
          selected: { backend: 'openai_api', model: 'gpt-transcribe' }
        })
      };
    }
    attempts++;
    if (attempts === 1) {
      // Mid-stream EOF after delta without transcript.text.done or error.
      return makeSSEResponse([
        'event: transcript.text.delta\ndata: {"type":"transcript.text.delta","delta":"partial only"}\n\n'
      ]);
    }
    return makeSSEResponse([
      'event: transcript.text.delta\ndata: {"type":"transcript.text.delta","delta":" full"}\n\n',
      'event: transcript.text.done\ndata: {"type":"transcript.text.done","text":" full"}\n\n'
    ]);
  };

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    fetch: fakeFetch,
    mediaDevices: media.mediaDevices,
    MediaRecorder: media.MediaRecorder,
    autoDiscover: false
  });
  await handle.controller.refreshCapabilities();

  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();
  await flush();

  assert.equal(env.ctext.value, 'keep me', 'incomplete stream must never commit partial text');
  assert.equal(env.composer.hasAttribute('data-voice-locked'), false, 'composer unlocks after incomplete stream');
  assert.equal(env.cap.getAttribute('data-state'), 'error');
  assert.equal(handle.controller.canRetry(), true, 'in-memory last recording preserved for resend');
  const resendBtn = env.cap.querySelector('[data-voice-action="resend"]');
  assert.ok(resendBtn, 'resend button offered on incomplete stream');

  resendBtn.click();
  await flush();
  await flush();
  await flush();
  assert.equal(env.ctext.value, 'keep me full');
  assert.equal(handle.controller.canRetry(), false);
  handle.destroy();
});
