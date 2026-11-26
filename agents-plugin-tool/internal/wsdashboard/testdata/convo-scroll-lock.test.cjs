"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const dashSrc = fs.readFileSync(path.join(assetsDir, "dashboard.js"), "utf8");
const layoutCss = fs.readFileSync(path.join(assetsDir, "dashboard-layout.css"), "utf8");
const convoCss = fs.readFileSync(path.join(assetsDir, "dashboard-conversation.css"), "utf8");
const tmplSrc = fs.readFileSync(path.join(assetsDir, "dashboard.tmpl"), "utf8");

function extractScrollSection(src) {
  const start = src.indexOf("  var scrollSnap = null;");
  const end = src.indexOf("\n  // --- layout and tabs", start);
  assert.ok(start >= 0 && end > start, "dashboard.js scroll section not found");
  return src.slice(start, end);
}

function createHarness(opts) {
  const options = opts || {};
  const rafQueue = [];
  let layoutReads = 0;
  let btnWrites = 0;
  let resizeCb = null;

  function makeEmitter() {
    const map = new Map();
    return {
      listeners: map,
      addEventListener(type, fn, opts) {
        if (!map.has(type)) map.set(type, []);
        map.get(type).push({ fn, opts });
      },
      dispatch(type, event) {
        const list = map.get(type) || [];
        for (const item of list) item.fn(event || {});
      },
    };
  }

  const winEmitter = makeEmitter();
  const convoEmitter = makeEmitter();
  const btnEmitter = makeEmitter();

  let _scrollTop = options.scrollTop ?? 900;
  let _scrollHeight = options.scrollHeight ?? 1200;
  let _clientHeight = options.clientHeight ?? 300;
  const convo = {
    isConnected: true,
    wbScrollOwner: false,
    addEventListener: convoEmitter.addEventListener,
    dispatch: convoEmitter.dispatch,
    get scrollTop() { return _scrollTop; },
    set scrollTop(v) {
      const max = Math.max(0, _scrollHeight - _clientHeight);
      const clamped = Math.max(0, Math.min(max, v));
      const changed = clamped !== _scrollTop;
      _scrollTop = clamped;
      if (changed && options.syncScrollOnWrite) convoEmitter.dispatch("scroll");
    },
    get scrollHeight() { layoutReads++; return _scrollHeight; },
    set scrollHeight(v) { _scrollHeight = v; },
    get clientHeight() { layoutReads++; return _clientHeight; },
    set clientHeight(v) { _clientHeight = v; },
    setRawScrollTop(v) { _scrollTop = v; },
  };

  const btnClasses = new Set();
  const btnAttrs = { "aria-hidden": "true" };
  let btnTabIndex = -1;
  const convoBottomBtn = {
    addEventListener: btnEmitter.addEventListener,
    dispatch: btnEmitter.dispatch,
    classList: {
      toggle(cls, force) {
        btnWrites++;
        if (force) btnClasses.add(cls);
        else btnClasses.delete(cls);
      },
      contains(cls) { return btnClasses.has(cls); },
    },
    setAttribute(k, v) { btnWrites++; btnAttrs[k] = String(v); },
    getAttribute(k) { return btnAttrs[k]; },
    get tabIndex() { return btnTabIndex; },
    set tabIndex(v) { btnWrites++; btnTabIndex = v; },
  };

  const elements = {
    convo,
    "convo-bottom-btn": convoBottomBtn,
  };

  function FakeResizeObserver(cb) {
    resizeCb = cb;
    return { observe() {} };
  }
  const sandbox = {
    PANES: ["convo"],
    paneObserver: null,
    paneVisible: { convo: true },
    visible: (el) => el === convo,
    $: (id) => elements[id] || null,
    doc: { scrollingElement: null },
    ResizeObserver: FakeResizeObserver,
    window: {
      addEventListener: winEmitter.addEventListener,
      wbHistory: null,
      ResizeObserver: FakeResizeObserver,
    },
    requestAnimationFrame(fn) {
      rafQueue.push(fn);
      return rafQueue.length;
    },
    Date,
    Math,
    Map,
    Object,
  };
  sandbox.globalThis = sandbox;

  const section = extractScrollSection(dashSrc);
  const exportsTrailer = `
    ;globalThis.__api = {
      get stick() { return stick; },
      set stick(v) { stick = v; },
      get convoSwapping() { return convoSwapping; },
      set convoSwapping(v) { convoSwapping = v; },
      get scrollSnap() { return scrollSnap; },
      get pinConvoProgrammatic() { return pinConvoProgrammatic; },
      scrollState,
      restoreScroll,
      setConvoScrollTop,
      pinConvo,
      queuePinConvo,
      followConvo,
      syncConvoBottom,
      queueSyncConvoBottom,
      atBottom,
      pinned,
    };
  `;
  vm.runInNewContext(section + exportsTrailer, sandbox, { filename: "dashboard-scroll.js" });

  function flushRaf() {
    let guard = 0;
    while (rafQueue.length && guard++ < 20) {
      const batch = rafQueue.splice(0, rafQueue.length);
      for (const fn of batch) fn();
    }
  }

  return {
    api: sandbox.__api,
    convo,
    btn: convoBottomBtn,
    win: winEmitter,
    flushRaf,
    triggerResize() { if (resizeCb) resizeCb(); },
    get layoutReads() { return layoutReads; },
    resetLayoutReads() { layoutReads = 0; },
    get btnWrites() { return btnWrites; },
    resetBtnWrites() { btnWrites = 0; },
  };
}

test("stick=false stays false across convlive shrink/clamp swaps and restoreScroll preserves reading position", () => {
  const h = createHarness({ scrollTop: 900, scrollHeight: 1200, clientHeight: 300 });
  assert.equal(h.api.stick, true);

  // User scrolls up to read earlier messages.
  h.convo.dispatch("wheel", { deltaY: -120 });
  h.convo.setRawScrollTop(300);
  h.convo.dispatch("scroll");
  h.flushRaf();

  assert.equal(h.api.stick, false);
  assert.equal(h.btn.classList.contains("is-shown"), true);
  assert.equal(h.btn.getAttribute("aria-hidden"), "false");
  assert.equal(h.btn.tabIndex, 0);

  // Swap shrinks #r-convlive (streaming turn clears before committed entry renders).
  const snap = h.api.scrollState();
  h.api.convoSwapping++;
  h.convo.scrollHeight = 500;
  // Browser clamps scrollTop to 200 (temporary bottom!) and fires scroll both sync and async.
  h.convo.setRawScrollTop(200);
  h.convo.dispatch("scroll");
  h.api.convoSwapping--;
  // Async scroll event delivered after swap finishes with no downward user gesture.
  h.convo.dispatch("scroll");
  h.flushRaf();

  assert.equal(h.api.stick, false, "shrink/clamp swap must never flip stick from false to true");

  // Committed entry arrives, growing scrollHeight back to 1400, and restoreScroll restores 300.
  h.convo.scrollHeight = 1400;
  h.api.restoreScroll(snap);
  h.flushRaf();
  // Async scroll event from restoreScroll's programmatic write.
  h.convo.dispatch("scroll");
  h.flushRaf();

  assert.equal(h.convo.scrollTop, 300, "restoreScroll must preserve user's reading scrollTop");
  assert.equal(h.api.stick, false, "programmatic restoreScroll must not re-arm stick");
  assert.equal(h.btn.classList.contains("is-shown"), true);

  // ResizeObserver clamp at temporary bottom must also never flip stick to true.
  h.convo.scrollHeight = 500;
  h.convo.setRawScrollTop(200);
  h.triggerResize();
  h.convo.dispatch("scroll");
  h.flushRaf();
  assert.equal(h.api.stick, false, "resize clamp must not flip stick from false to true");
});

test("user scroll input invalidates scrollSnap in the same rAF frame without layout reads", () => {
  const h = createHarness({ scrollTop: 800, scrollHeight: 1400, clientHeight: 300 });
  h.api.stick = false;

  // Unrelated region swap captures scrollState and queues restoreScroll for this frame.
  const snap = h.api.scrollState();
  assert.ok(h.api.scrollSnap !== null);
  h.api.restoreScroll(snap);

  // User scrolls up in the same animation frame before rAF runs.
  h.resetLayoutReads();
  h.win.dispatch("wheel", { deltaY: -100 });
  h.convo.dispatch("wheel", { deltaY: -100 });
  assert.equal(h.layoutReads, 0, "passive input listeners must not trigger layout reads");
  assert.equal(h.api.scrollSnap, null, "scrollSnap must be cleared immediately on user input");

  h.convo.setRawScrollTop(250);
  h.convo.dispatch("scroll");
  h.flushRaf();

  assert.equal(h.convo.scrollTop, 250, "restoreScroll must not drag #convo back down to 800");
  assert.equal(h.api.stick, false);
});

test("genuine user scroll gestures to bottom re-arm stick=true and hide capsule", () => {
  const h = createHarness({ scrollTop: 900, scrollHeight: 1200, clientHeight: 300 });

  function scrollUp() {
    h.convo.dispatch("wheel", { deltaY: -120 });
    h.convo.setRawScrollTop(200);
    h.convo.dispatch("scroll");
    h.flushRaf();
    assert.equal(h.api.stick, false);
    assert.equal(h.btn.classList.contains("is-shown"), true);
  }

  // 1. Mouse wheel down to bottom.
  scrollUp();
  h.convo.dispatch("wheel", { deltaY: 120 });
  h.convo.setRawScrollTop(900);
  h.convo.dispatch("scroll");
  h.flushRaf();
  assert.equal(h.api.stick, true);
  assert.equal(h.btn.classList.contains("is-shown"), false);

  // 2. Scrollbar pointer drag to bottom.
  scrollUp();
  h.convo.dispatch("pointerdown");
  h.convo.setRawScrollTop(900);
  h.convo.dispatch("scroll");
  h.win.dispatch("pointerup");
  h.flushRaf();
  assert.equal(h.api.stick, true);
  assert.equal(h.btn.classList.contains("is-shown"), false);

  // 3. Keyboard End key to bottom.
  scrollUp();
  h.convo.dispatch("keydown", { key: "End" });
  h.convo.setRawScrollTop(900);
  h.convo.dispatch("scroll");
  h.flushRaf();
  assert.equal(h.api.stick, true);
  assert.equal(h.btn.classList.contains("is-shown"), false);
});

test("jump-to-bottom capsule visibility toggles cleanly, avoids redundant writes, and click follows bottom", () => {
  const h = createHarness({ scrollTop: 0, scrollHeight: 300, clientHeight: 300 });

  // No overflow: even when stick=false, capsule stays hidden.
  h.api.stick = false;
  h.api.syncConvoBottom();
  assert.equal(h.btn.classList.contains("is-shown"), false);

  // Add overflow, still within 24px bottom threshold: stays hidden (no jitter).
  h.convo.scrollHeight = 1200;
  h.convo.setRawScrollTop(880); // 1200 - 300 - 880 = 20 <= 24
  h.api.syncConvoBottom();
  assert.equal(h.btn.classList.contains("is-shown"), false);

  // Scroll above threshold: capsule pops in.
  h.resetBtnWrites();
  h.convo.setRawScrollTop(400);
  h.api.syncConvoBottom();
  assert.equal(h.btn.classList.contains("is-shown"), true);
  assert.equal(h.btn.getAttribute("aria-hidden"), "false");
  assert.equal(h.btn.tabIndex, 0);
  const writesAfterShow = h.btnWrites;
  assert.ok(writesAfterShow > 0);

  // Repeated syncs while state is unchanged perform zero redundant DOM writes.
  h.api.syncConvoBottom();
  h.api.syncConvoBottom();
  h.convo.dispatch("scroll");
  h.flushRaf();
  assert.equal(h.btnWrites, writesAfterShow, "unchanged visibility must not write DOM");

  // Clicking capsule calls followConvo(), jumps to bottom, sets stick=true, and hides immediately.
  h.btn.dispatch("click");
  assert.equal(h.api.stick, true);
  assert.equal(h.convo.scrollTop, 900);
  assert.equal(h.btn.classList.contains("is-shown"), false);
  assert.equal(h.btn.getAttribute("aria-hidden"), "true");
  assert.equal(h.btn.tabIndex, -1);

  // While pinned (stick=true), syncConvoBottom short-circuits without layout reads.
  h.resetLayoutReads();
  h.api.syncConvoBottom();
  assert.equal(h.layoutReads, 0, "pinned syncConvoBottom must not read layout");
});

test("CSS and template contract for overflow-anchor and floating bottom-center capsule", () => {
  assert.match(layoutCss, /\.dgrid\.history\s*>\s*\.convo\s*\{[^}]*overflow-anchor:\s*none;/);
  assert.match(layoutCss, /\.dgrid\.live\s*>\s*\.convo\s*\{[^}]*overflow-anchor:\s*none;/);
  assert.match(convoCss, /\.dgrid\.live\s*>\s*\.d-convo-bottom\s*\{[^}]*grid-area:\s*convo;[^}]*align-self:\s*end;[^}]*justify-self:\s*center;/);
  assert.match(convoCss, /\.dgrid\.live\s*>\s*\.d-convo-bottom\.is-shown\s*\{[^}]*cubic-bezier\(\.34,\s*1\.56,\s*\.64,\s*1\)/);
  assert.match(convoCss, /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[^}]*\.d-convo-bottom/);
  assert.match(tmplSrc, /id="convo-bottom-btn"[^>]*aria-label="\{\{t "js\.convo\.jump\.bottom"\}\}"[^>]*title="\{\{t "js\.convo\.jump\.bottom"\}\}"/);
});
