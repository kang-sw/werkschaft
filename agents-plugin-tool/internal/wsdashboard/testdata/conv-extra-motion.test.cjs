const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.resolve(__dirname, "../assets");
const motionSource = fs.readFileSync(path.join(assetsDir, "dashboard-conv-motion.js"), "utf8");
const dashboardSource = fs.readFileSync(path.join(assetsDir, "dashboard.js"), "utf8");

function makeCanvasCtx(log) {
  const grad = { addColorStop() {} };
  return {
    setTransform() {},
    clearRect(x, y, w, h) { log.clears++; },
    save() {},
    restore() {},
    translate() {},
    rotate() {},
    scale() {},
    beginPath() {},
    closePath() {},
    moveTo() {},
    lineTo() {},
    arc(x, y, r) { log.arcs.push({ x, y, r }); },
    ellipse() {},
    rect() {},
    fill() { log.fills++; },
    stroke() { log.strokes++; },
    fillText(text, x, y) { log.texts.push({ text, x, y }); },
    measureText(text) { return { width: String(text || "").length * 7 }; },
    createRadialGradient() { return grad; },
    createLinearGradient() { return grad; }
  };
}

function makeFakeElement(tag, opts = {}) {
  const attrs = Object.assign({}, opts.attrs || {});
  const classes = new Set(opts.classes || []);
  const listeners = Object.create(null);
  const children = [];
  const el = {
    tagName: tag.toUpperCase(),
    id: opts.id || "",
    className: "",
    textContent: opts.textContent || "",
    title: "",
    hidden: !!opts.hidden,
    parentNode: null,
    children,
    _rect: opts.rect || { left: 0, top: 0, width: 100, height: 24, right: 100, bottom: 24 },
    _ctxLog: { clears: 0, fills: 0, strokes: 0, arcs: [], texts: [] },
    classList: {
      add(...cs) { cs.forEach(c => classes.add(c)); },
      remove(...cs) { cs.forEach(c => classes.delete(c)); },
      contains(c) { return classes.has(c); },
      toggle(c, on) { if (on === undefined) on = !classes.has(c); if (on) classes.add(c); else classes.delete(c); return on; }
    },
    setAttribute(k, v) { attrs[k] = String(v); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k); },
    removeAttribute(k) { delete attrs[k]; },
    appendChild(child) {
      child.parentNode = el;
      children.push(child);
      return child;
    },
    removeChild(child) {
      const idx = children.indexOf(child);
      if (idx >= 0) children.splice(idx, 1);
      child.parentNode = null;
      return child;
    },
    addEventListener(ev, fn) { (listeners[ev] ||= []).push(fn); },
    removeEventListener(ev, fn) {
      if (!listeners[ev]) return;
      listeners[ev] = listeners[ev].filter(f => f !== fn);
    },
    dispatchEvent(ev) {
      (listeners[ev.type] || []).slice().forEach(fn => fn(ev));
    },
    getBoundingClientRect() {
      const r = el._rect;
      return { left: r.left, top: r.top, width: r.width, height: r.height, right: r.left + r.width, bottom: r.top + r.height };
    },
    getContext() {
      if (!el._ctx) el._ctx = makeCanvasCtx(el._ctxLog);
      return el._ctx;
    },
    closest(sel) {
      if (sel === "[data-motion-mute]" && attrs["data-motion-mute"]) return el;
      return el.parentNode && el.parentNode.closest ? el.parentNode.closest(sel) : null;
    },
    querySelector(sel) {
      const all = el.querySelectorAll(sel);
      return all[0] || null;
    },
    querySelectorAll(sel) {
      const res = [];
      function matches(node) {
        if (sel === ".clive") return node.classList.contains("clive");
        if (sel === ".activity" || sel === ".clive .activity") return node.classList.contains("activity");
        if (sel === ".spin") return node.classList.contains("spin");
        if (sel === "[data-motion-mute]") return node.hasAttribute("data-motion-mute");
        if (sel === ".d-turn-live [data-conv-turn-tokens]" || sel === "[data-conv-turn-tokens]") return node.hasAttribute("data-conv-turn-tokens");
        if (sel === "[data-conv-turn-summary]") return node.hasAttribute("data-conv-turn-summary");
        return false;
      }
      function walk(node) {
        for (const ch of node.children) {
          if (matches(ch)) res.push(ch);
          walk(ch);
        }
      }
      walk(el);
      return res;
    }
  };
  return el;
}

function createHarness(opts = {}) {
  const docListeners = Object.create(null);
  const winListeners = Object.create(null);
  const storageMap = new Map(Object.entries(opts.initialStorage || {}));
  let rafSeq = 0;
  const rafQueue = new Map();
  let nowMs = 100000;
  let perfMs = 1000;

  const root = makeFakeElement("html", { attrs: { "data-theme": opts.theme || "dark" } });
  const grid = makeFakeElement("main", { id: "grid", classes: ["dgrid", "live"], attrs: { "data-layout": "auto", "data-tab": "convo", "data-ctab": "convo" }, rect: { left: 0, top: 0, width: 520, height: 320 } });
  const convo = makeFakeElement("section", { id: "convo", classes: ["convo"], rect: { left: 0, top: 0, width: 520, height: 320 } });
  const convlive = makeFakeElement("div", { id: "r-convlive" });
  convo.appendChild(convlive);
  grid.appendChild(convo);

  const gmenuBtnOn = makeFakeElement("button", { attrs: { "data-extra-motion": "on", "aria-pressed": "true" } });
  const gmenuBtnOff = makeFakeElement("button", { attrs: { "data-extra-motion": "off", "aria-pressed": "false" } });

  const doc = {
    hidden: !!opts.hidden,
    documentElement: root,
    body: makeFakeElement("body"),
    createElement(tag) { return makeFakeElement(tag); },
    getElementById(id) {
      if (id === "convo") return convo;
      if (id === "grid") return grid;
      if (id === "r-convlive") return convlive;
      return null;
    },
    addEventListener(ev, fn) { (docListeners[ev] ||= []).push(fn); },
    removeEventListener(ev, fn) {
      if (!docListeners[ev]) return;
      docListeners[ev] = docListeners[ev].filter(f => f !== fn);
    },
    dispatchEvent(ev) {
      (docListeners[ev.type] || []).slice().forEach(fn => fn(ev));
    }
  };

  let reducedMatches = !!opts.reducedMotion;
  const mqlListeners = [];
  const mql = {
    get matches() { return reducedMatches; },
    addEventListener(ev, fn) { if (ev === "change") mqlListeners.push(fn); },
    removeEventListener(ev, fn) {
      const idx = mqlListeners.indexOf(fn);
      if (idx >= 0) mqlListeners.splice(idx, 1);
    },
    setMatches(v) {
      reducedMatches = !!v;
      mqlListeners.slice().forEach(fn => fn({ matches: reducedMatches }));
    }
  };

  const win = {
    devicePixelRatio: 2,
    performance: { now: () => perfMs },
    localStorage: {
      getItem: k => (storageMap.has(k) ? storageMap.get(k) : null),
      setItem: (k, v) => { storageMap.set(k, String(v)); },
      removeItem: k => { storageMap.delete(k); }
    },
    matchMedia(q) {
      if (q.includes("prefers-reduced-motion")) return mql;
      return { matches: (opts.theme || "dark") === "dark", addEventListener() {}, removeEventListener() {} };
    },
    requestAnimationFrame(cb) {
      const id = ++rafSeq;
      rafQueue.set(id, cb);
      return id;
    },
    cancelAnimationFrame(id) {
      rafQueue.delete(id);
    },
    addEventListener(ev, fn) { (winListeners[ev] ||= []).push(fn); },
    removeEventListener(ev, fn) {
      if (!winListeners[ev]) return;
      winListeners[ev] = winListeners[ev].filter(f => f !== fn);
    }
  };

  class CustomEventPolyfill {
    constructor(type, init) { this.type = type; this.detail = init && init.detail; }
  }

  let extraMotionState = opts.initialExtraMotion !== undefined ? !!opts.initialExtraMotion : true;
  function setExtraMotionMock(v, user) {
    extraMotionState = !(v === false || v === "off" || v === "0" || v === "false");
    const attr = extraMotionState ? "on" : "off";
    root.setAttribute("data-extra-motion", attr);
    gmenuBtnOn.setAttribute("aria-pressed", String(attr === "on"));
    gmenuBtnOff.setAttribute("aria-pressed", String(attr === "off"));
    if (user) win.localStorage.setItem("ws.dashboard.extraMotion", attr);
    doc.dispatchEvent(new CustomEventPolyfill("ws-extra-motion", { detail: { enabled: extraMotionState } }));
  }

  const timerStats = { arms: 0, delays: [] };
  const fakeSetTimeout = (fn, ms) => {
    timerStats.arms++;
    timerStats.delays.push(ms);
    return setTimeout(fn, ms);
  };
  class FakeDate extends Date {
    static now() { return nowMs; }
  }

  const sandbox = {
    window: win,
    document: doc,
    doc,
    root,
    convo,
    grid,
    extraMotion: extraMotionState,
    shHidden: false,
    setExtraMotion: setExtraMotionMock,
    wsT: k => ({ "js.motion.off": "모션 끄기", "js.motion.off.title": "Extra Motion 끄기" }[k] || k),
    CustomEvent: CustomEventPolyfill,
    setTimeout: fakeSetTimeout,
    clearTimeout,
    Math,
    Date: FakeDate,
    parseInt,
    String,
    Object,
    Array,
    Number,
    console
  };
  win.window = win;
  vm.runInNewContext(motionSource, sandbox);

  const overlay = grid.children.find(c => c.className === "d-conv-motion-overlay");
  assert.ok(overlay, "overlay wrapper mounted on swap-stable #grid host");
  assert.equal(overlay.getAttribute("aria-hidden"), "true");
  assert.equal(overlay.hidden, true);
  const canvas = overlay.children.find(c => c.tagName === "CANVAS");
  assert.ok(canvas, "canvas mounted inside .d-conv-motion-overlay");
  canvas._rect = { left: 0, top: 0, width: 520, height: 320 };

  function setLiveDOM({ phase = "idle", tool = "", done = 0, failed = 0, turn = "100", turnTokens = 0, estimated = false, activityAt = nowMs, hasActivity = false, tokenX = 300, tokenWidth = 60, hasFails = false }) {
    convlive.children.length = 0;
    const clive = makeFakeElement("div", {
      classes: ["conv", "clive"],
      attrs: {
        "data-phase": phase,
        "data-done": String(done),
        "data-failed": String(failed),
        "data-turn": String(turn),
        "data-turn-tokens": String(turnTokens),
        "data-activity-at": String(activityAt)
      }
    });
    if (tool) clive.setAttribute("data-tool", tool);

    if (done > 0 || failed > 0 || turnTokens > 0 || estimated) {
      const turnEl = makeFakeElement("details", { classes: ["toolsline", "d-turn", "live", "d-turn-live"] });
      const sumEl = makeFakeElement("summary", { classes: ["conv-row", "d-turn-sum"], attrs: { "data-conv-turn-summary": "" }, rect: { left: 12, top: 14, width: 420, height: 22 } });
      if (hasFails) {
        const failEl = makeFakeElement("span", { classes: ["d-turn-fails"], attrs: { "data-conv-turn-fails": "" }, textContent: "실패 " + failed });
        sumEl.appendChild(failEl);
      }
      const tokAttrs = { "data-conv-turn-tokens": "" };
      if (estimated) tokAttrs["data-estimated"] = "1";
      else if (turnTokens > 0) tokAttrs["data-tokens"] = String(turnTokens);
      const tokEl = makeFakeElement("span", {
        classes: ["d-turn-tok"],
        attrs: tokAttrs,
        textContent: estimated ? "~+1.1k tok" : ("+" + turnTokens + " tok"),
        rect: { left: tokenX, top: 14, width: tokenWidth, height: 20 }
      });
      sumEl.appendChild(tokEl);
      turnEl.appendChild(sumEl);
      clive.appendChild(turnEl);
    }

    if (hasActivity) {
      const act = makeFakeElement("div", { classes: ["activity"], rect: { left: 14, top: 120, width: 260, height: 22 } });
      const spin = makeFakeElement("span", { classes: ["spin"], rect: { left: 16, top: 122, width: 24, height: 18 } });
      act.appendChild(spin);
      clive.appendChild(act);
    }
    convlive.appendChild(clive);
    sandbox.convMotionSync();
    return clive;
  }

  function stepRaf(frames = 1, dtMs = 16) {
    for (let i = 0; i < frames; i++) {
      perfMs += dtMs;
      const entries = Array.from(rafQueue.entries());
      rafQueue.clear();
      for (const [, cb] of entries) cb(perfMs);
    }
  }

  return {
    WsConvMotion: sandbox.WsConvMotion,
    engine: sandbox.WsConvMotion.getInstance(),
    doc,
    win,
    root,
    grid,
    convo,
    convlive,
    canvas,
    mql,
    rafQueue,
    storageMap,
    gmenuBtnOn,
    gmenuBtnOff,
    setExtraMotion: setExtraMotionMock,
    setLiveDOM,
    convMotionSync: sandbox.convMotionSync,
    timerStats,
    stepRaf,
    now: () => nowMs,
    advanceNow(ms) { nowMs += ms; }
  };
}

test("classifyTool maps all 5 tool families and falls back unknown tools to file", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  const { classifyTool } = h.WsConvMotion;

  assert.equal(classifyTool("ws/exec.shell"), "exec");
  assert.equal(classifyTool("ws/exec.run"), "exec");
  assert.equal(classifyTool("mcp_ws_exec_shell"), "exec");
  assert.equal(classifyTool("run_command"), "exec");
  assert.equal(classifyTool("bash"), "exec");

  assert.equal(classifyTool("ws/tickets.query"), "tickets");
  assert.equal(classifyTool("ws/tickets.move"), "tickets");
  assert.equal(classifyTool("ws/whiteboard.write"), "tickets");
  assert.equal(classifyTool("ws/todo.add"), "tickets");
  assert.equal(classifyTool("ws/agenda.set"), "tickets");
  assert.equal(classifyTool("ws/playbook.render"), "tickets");

  assert.equal(classifyTool("ws/agent.spawn"), "agent");
  assert.equal(classifyTool("ws/mailbox.send"), "agent");
  assert.equal(classifyTool("mcp_ws_agent_list"), "agent");
  assert.equal(classifyTool("swarm"), "agent");

  assert.equal(classifyTool("ws/git.commit"), "git");
  assert.equal(classifyTool("ws/git.diff"), "git");
  assert.equal(classifyTool("ws/worktree.acquire"), "git");
  assert.equal(classifyTool("ws/rationale.query"), "git");

  assert.equal(classifyTool("read_file"), "file");
  assert.equal(classifyTool("edit_file"), "file");
  assert.equal(classifyTool("grep_search"), "file");
  assert.equal(classifyTool("custom_unknown_tool"), "file");
  assert.equal(classifyTool(""), "file");
  h.engine.destroy();
});

test("state machine transitions idle -> thinking -> pop-in -> sway -> OK/FAIL flights -> +m impact -> 0 rAF", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  assert.equal(h.rafQueue.size, 0, "idle starts at 0 rAF");
  assert.equal(h.engine.getState().canvasHidden, true);

  // 1. Thinking state activates rAF and shows canvas
  h.setLiveDOM({ phase: "thinking", done: 1, failed: 0, turn: "t1", turnTokens: 1000, hasActivity: true });
  assert.equal(h.engine.getState().runnerMode, "thinking");
  assert.equal(h.engine.getState().catId, "thinking");
  assert.equal(h.rafQueue.size, 1, "thinking schedules 1 rAF");
  assert.equal(h.engine.getState().canvasHidden, false);
  h.stepRaf(5, 34);

  // 2. Thinking -> tool triggers pop-in + sway
  h.setLiveDOM({ phase: "tool", tool: "ws/exec.shell", done: 1, failed: 0, turn: "t1", turnTokens: 1000, hasActivity: true });
  assert.equal(h.engine.getState().runnerMode, "sway");
  assert.equal(h.engine.getState().catId, "exec");
  h.stepRaf(10, 16);

  // 3. Tool completes OK with +340 exact token increase -> launches OK flight and next tool pops in
  h.setLiveDOM({ phase: "tool", tool: "ws/git.diff", done: 2, failed: 0, turn: "t1", turnTokens: 1340, hasActivity: true });
  assert.equal(h.engine.getState().flights, 1, "OK flight launched");
  assert.equal(h.engine.getState().catId, "git", "next tool category active");

  // Advance until OK flight impacts and emits "+340" popup
  let sawOkPopup = false;
  for (let i = 0; i < 90; i++) {
    h.stepRaf(1, 16);
    const pops = h.engine.getState().popups;
    if (pops.some(p => p.text === "+340" && !p.jitter)) sawOkPopup = true;
  }
  assert.equal(sawOkPopup, true, "OK impact spawns floating emerald +340 popup matching exact token delta");

  // 4. Second tool FAILS with +180 exact token increase while turn transitions to idle
  h.setLiveDOM({ phase: "idle", done: 2, failed: 1, turn: "t1", turnTokens: 1520, hasActivity: false, hasFails: true, tokenX: 355 });
  assert.equal(h.engine.getState().flights, 1, "FAIL flight launched on completion");
  assert.equal(h.engine.getState().runnerMode, "idle");

  let sawFailPopup = false;
  for (let i = 0; i < 160; i++) {
    h.stepRaf(1, 20);
    const pops = h.engine.getState().popups;
    if (pops.some(p => p.text === "+180" && p.jitter)) sawFailPopup = true;
  }
  assert.equal(sawFailPopup, true, "FAIL impact spawns jittering coral-red +180 popup");
  // Let popups/impacts finish completely
  h.stepRaf(120, 25);
  assert.equal(h.rafQueue.size, 0, "rAF cancels back to 0 once flight, impact, and popup settle");
  assert.equal(h.engine.getState().canvasHidden, true, "canvas hides on idle");
  h.engine.destroy();
});

test("mixed OK+FAIL completion batch still launches a FAIL flight (coral +m popup)", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  h.setLiveDOM({ phase: "thinking", done: 1, failed: 0, turn: "t9", turnTokens: 1000, hasActivity: true });
  h.stepRaf(3, 16);
  h.setLiveDOM({ phase: "tool", tool: "ws/exec.shell", done: 1, failed: 0, turn: "t9", turnTokens: 1000, hasActivity: true });
  h.stepRaf(5, 16);
  h.setLiveDOM({ phase: "tool", tool: "ws/git.diff", done: 2, failed: 1, turn: "t9", turnTokens: 1250, hasActivity: true, hasFails: true });
  assert.equal(h.engine.getState().flights, 2, "one OK and one FAIL flight launched");
  let sawFailPopup = false;
  for (let i = 0; i < 200; i++) {
    h.stepRaf(1, 16);
    if (h.engine.getState().popups.some(p => p.text === "+250" && p.jitter)) sawFailPopup = true;
  }
  assert.equal(sawFailPopup, true, "last flight in a mixed batch is FAIL and carries the jittering +m popup");
});

test("+m popup is omitted when exact tokens did not advance or only ~ estimate exists", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  // Exact tokens unchanged (1000 -> 1000)
  h.setLiveDOM({ phase: "tool", tool: "ws/tickets.query", done: 1, turn: "t2", turnTokens: 1000, hasActivity: true });
  h.stepRaf(4, 16);
  h.setLiveDOM({ phase: "idle", done: 2, turn: "t2", turnTokens: 1000, hasActivity: false });
  assert.equal(h.engine.getState().flights, 1);
  let sawAnyPopup = false;
  for (let i = 0; i < 120; i++) {
    h.stepRaf(1, 20);
    if (h.engine.getState().popups.length > 0) sawAnyPopup = true;
  }
  assert.equal(sawAnyPopup, false, "no +m popup when token counter did not advance");
  assert.equal(h.rafQueue.size, 0);

  // Estimated (~) token header only
  h.setLiveDOM({ phase: "tool", tool: "ws/agent.spawn", done: 0, turn: "t3", turnTokens: 500, estimated: true, hasActivity: true });
  h.stepRaf(4, 16);
  h.setLiveDOM({ phase: "idle", done: 1, turn: "t3", turnTokens: 900, estimated: true, hasActivity: false });
  assert.equal(h.engine.getState().flights, 1);
  sawAnyPopup = false;
  for (let i = 0; i < 120; i++) {
    h.stepRaf(1, 20);
    if (h.engine.getState().popups.length > 0) sawAnyPopup = true;
  }
  assert.equal(sawAnyPopup, false, "no +m popup when only ~ estimate is present");
  assert.equal(h.rafQueue.size, 0);
  h.engine.destroy();
});

test("0 rAF enforced on hover mute, Settings OFF, visibilitychange hidden, prefers-reduced-motion, and stale busy state", async t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  const clive = h.setLiveDOM({ phase: "tool", tool: "ws/exec.shell", done: 1, turn: "t4", turnTokens: 400, hasActivity: true });
  assert.equal(h.rafQueue.size, 1);
  const act = clive.querySelector(".activity");
  const muteBtn = act.querySelector("[data-motion-mute]");
  assert.ok(muteBtn, "inline hover mute button injected inside .activity");
  assert.equal(act.getAttribute("data-motion-active"), "1");

  // 1. Clicking inline hover "모션 끄기" cancels rAF, hides canvas, switches to static dot, and syncs Settings + localStorage
  h.doc.dispatchEvent({ type: "click", target: muteBtn, preventDefault() {}, stopPropagation() {} });
  assert.equal(h.rafQueue.size, 0, "hover mute immediately cancels rAF to 0");
  assert.equal(h.engine.getState().canvasHidden, true);
  assert.equal(h.storageMap.get("ws.dashboard.extraMotion"), "off");
  assert.equal(h.gmenuBtnOff.getAttribute("aria-pressed"), "true");
  assert.equal(h.gmenuBtnOn.getAttribute("aria-pressed"), "false");
  assert.equal(act.getAttribute("data-motion-fallback"), "1");

  // 2. Re-enable via Settings popover restarts rAF cleanly, then Settings OFF stops it again
  h.setExtraMotion("on", true);
  assert.equal(h.rafQueue.size, 1, "re-enabling via Settings resumes active tool motion");
  h.setExtraMotion("off", true);
  assert.equal(h.rafQueue.size, 0, "Settings OFF cancels rAF to 0");

  // 3. visibilitychange hidden pauses rAF to 0, visible resumes
  h.setExtraMotion("on", true);
  assert.equal(h.rafQueue.size, 1);
  h.doc.hidden = true;
  h.doc.dispatchEvent({ type: "visibilitychange" });
  assert.equal(h.rafQueue.size, 0, "document.hidden cancels rAF to 0");
  h.doc.hidden = false;
  h.doc.dispatchEvent({ type: "visibilitychange" });
  assert.equal(h.rafQueue.size, 1, "returning visible resumes active motion");

  // 4. prefers-reduced-motion: reduce forces 0 rAF and static dot fallback
  h.mql.setMatches(true);
  assert.equal(h.rafQueue.size, 0, "prefers-reduced-motion forces 0 rAF");
  assert.equal(act.getAttribute("data-motion-fallback"), "1");
  h.mql.setMatches(false);
  assert.equal(h.rafQueue.size, 1);
  h.engine.destroy();

  // 5. Stale busy/thinking state drops rAF loop to 0 after staleMs without live updates
  const h2 = createHarness();
  const staleEng = h2.WsConvMotion.createEngine({
    doc: h2.doc, win: h2.win, root: h2.root, convo: h2.convo, grid: h2.grid, convlive: h2.convlive, canvas: h2.canvas,
    staleMs: 35
  });
  h2.engine.destroy();
  staleEng.observe({ phase: "thinking", done: 0, failed: 0, turn: "t5", activityAt: h2.now(), hasActivity: true });
  assert.equal(staleEng.getState().rafActive, true);
  await new Promise(r => setTimeout(r, 70));
  assert.equal(staleEng.getState().stale, true, "busy state marked stale after timeout");
  assert.equal(staleEng.getState().rafActive, false, "stale busy state drops rAF to 0");
  assert.equal(h2.rafQueue.size, 0);
  staleEng.destroy();
});

test("dynamic target X shifts smoothly when [data-conv-turn-fails] appears", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());
  h.setLiveDOM({ phase: "tool", tool: "ws/git.commit", done: 1, failed: 0, turn: "t6", turnTokens: 1100, hasActivity: true, tokenX: 250, tokenWidth: 54 });
  h.stepRaf(3, 16);
  assert.equal(h.engine.getState().targetX, 277);

  // Tool fails -> [data-conv-turn-fails] appears, shifting [data-conv-turn-tokens] rightward by +70px!
  h.setLiveDOM({ phase: "idle", done: 1, failed: 1, turn: "t6", turnTokens: 1290, hasActivity: false, hasFails: true, tokenX: 320, tokenWidth: 54 });
  assert.equal(h.engine.getState().targetX, 347, "targetX re-measured at shifted [data-conv-turn-tokens] position");

  // Shift even further mid-flight and confirm impact popup tracks the live targetX
  const tokEl = h.convlive.querySelector("[data-conv-turn-tokens]");
  tokEl._rect = { left: 360, top: 14, width: 54, height: 20 };
  h.engine.relayout();
  let landedPopupX = 0;
  for (let i = 0; i < 140; i++) {
    h.stepRaf(1, 20);
    const pops = h.engine.getState().popups;
    if (pops.length) landedPopupX = pops[0].x;
  }
  assert.ok(landedPopupX > 380, `expected popup to converge near shifted targetX (387+), got ${landedPopupX}`);
  h.engine.destroy();
});

test("legacy ws.dashboard.layout of tabs or split loads as auto and is never written", () => {
  for (const staleVal of ["tabs", "split"]) {
    const writes = [];
    const store = new Map([["ws.dashboard.layout", staleVal]]);
    const gridAttrs = {};
    const bodyAttrs = {};
    const rootAttrs = {};
    const doc = {
      hidden: false,
      documentElement: {
        hasAttribute: k => Object.prototype.hasOwnProperty.call(rootAttrs, k),
        getAttribute: k => rootAttrs[k] || null,
        setAttribute: (k, v) => { rootAttrs[k] = String(v); },
        toggleAttribute: (k, on) => { if (on) rootAttrs[k] = ""; else delete rootAttrs[k]; }
      },
      body: { setAttribute: (k, v) => { bodyAttrs[k] = String(v); } },
      querySelectorAll: () => [],
      querySelector: () => null,
      addEventListener() {},
      dispatchEvent() {}
    };
    const grid = { setAttribute: (k, v) => { gridAttrs[k] = String(v); }, getAttribute: k => gridAttrs[k] || null };
    const fnMatch = dashboardSource.match(/function setLayout\([\s\S]*?\n  \}/);
    const parseMatch = dashboardSource.match(/var EXTRA_MOTION_KEY = [\s\S]*?root\.setAttribute\("data-extra-motion",[^\n]+/);
    assert.ok(fnMatch, "setLayout exists in dashboard.js");
    assert.ok(parseMatch, "EXTRA_MOTION_KEY early init exists in dashboard.js");

    const sandbox = {
      doc,
      root: doc.documentElement,
      grid,
      live: false,
      worktree: "",
      cardBoard: null,
      layoutMode: "auto",
      load: k => (store.has(k) ? store.get(k) : null),
      save: (k, v) => { writes.push([k, v]); store.set(k, v); },
      q: () => []
    };
    vm.runInNewContext(parseMatch[0] + "\n" + fnMatch[0] + "\nsetLayout(load('ws.dashboard.layout'));", sandbox);
    assert.equal(sandbox.layoutMode, "auto", `stale ${staleVal} must resolve to auto`);
    assert.equal(gridAttrs["data-layout"], "auto");
    assert.equal(bodyAttrs["data-dashboard-layout"], "auto");
    assert.equal(rootAttrs["data-extra-motion"], "on", "Extra Motion defaults to on");
    assert.equal(writes.some(([k]) => k === "ws.dashboard.layout"), false, "ws.dashboard.layout must never be written");
  }
});

test("overlay wrapper syncs visibility, observes convoEl via ResizeObserver, and cleans up on destroy", () => {
  let observed = [];
  let disconnected = 0;
  let lastCallback = null;
  class FakeResizeObserver {
    constructor(cb) { lastCallback = cb; }
    observe(el) { observed.push(el); }
    disconnect() { disconnected++; }
  }
  const h = createHarness();
  h.win.ResizeObserver = FakeResizeObserver;
  const overlay = h.grid.children.find(c => c.className === "d-conv-motion-overlay");
  assert.ok(overlay, "overlay exists on #grid");
  assert.equal(overlay.hidden, true, "overlay hidden while idle");

  h.setLiveDOM({ phase: "tool", tool: "Read", done: 1, turn: "ro1", hasActivity: true });
  assert.equal(overlay.hidden, false, "overlay visible while active");
  assert.equal(h.canvas.hidden, false, "canvas visible while active");
  assert.equal(observed.length, 1, "ResizeObserver attached to #convo when active");
  assert.equal(observed[0], h.convo);

  // Simulate #convo resize (e.g. composer image tray growing) -> markGeomDirty -> next step re-measures
  const tokEl = h.convlive.querySelector("[data-conv-turn-tokens]");
  tokEl._rect = { left: 220, top: 80, width: 60, height: 20 };
  lastCallback();
  h.stepRaf(2, 16);
  assert.equal(h.engine.getState().targetY, 90, "ResizeObserver invalidates cached geometry for next frame");

  // Going idle drains any transient popIn/trails, disconnects ResizeObserver, and hides overlay+canvas
  h.setLiveDOM({ phase: "idle", done: 1, turn: "ro1", hasActivity: false });
  h.stepRaf(120, 20);
  assert.equal(overlay.hidden, true, "overlay hidden when idle");
  assert.equal(h.canvas.hidden, true, "canvas hidden when idle");
  assert.equal(disconnected, 1, "ResizeObserver disconnected on stopLoop");

  h.engine.destroy();
  assert.equal(h.grid.children.includes(overlay), false, "destroy removes overlay wrapper from #grid");
});

test("already-stale data-activity-at enters stale immediately with 0 rAF and 0 stale timer armed", t => {
  const h = createHarness();
  t.after(() => h.engine.destroy());

  // Advance simulated clock past default 90s staleMs, then load a DOM whose
  // data-activity-at is already 120s old.
  h.advanceNow(200000);
  const oldActivityAt = h.now() - 120000;

  h.setLiveDOM({ phase: "thinking", done: 0, failed: 0, turn: "s1", activityAt: oldActivityAt, hasActivity: true });
  assert.equal(h.engine.getState().stale, true, "already-stale thinking state must enter stale immediately");
  assert.equal(h.engine.getState().rafActive, false, "already-stale thinking state must not start rAF");
  assert.equal(h.rafQueue.size, 0, "0 rAF frames queued for already-stale thinking state");
  assert.equal(h.timerStats.arms, 0, "stale timer must not be armed when already stale");

  // Switching to an already-stale tool phase also stays stale with 0 rAF / 0 timer arms.
  h.setLiveDOM({ phase: "tool", tool: "ws/exec.shell", done: 1, failed: 0, turn: "s1", activityAt: oldActivityAt + 1000, hasActivity: true });
  assert.equal(h.engine.getState().stale, true, "already-stale tool state must stay stale");
  assert.equal(h.engine.getState().rafActive, false);
  assert.equal(h.rafQueue.size, 0);
  assert.equal(h.timerStats.arms, 0, "stale timer must remain unarmed for already-stale tool phase");

  // Partially aged activity (60s old vs 90s staleMs) arms timer for remaining 30s, not full 90s.
  h.setLiveDOM({ phase: "thinking", done: 1, failed: 0, turn: "s2", activityAt: h.now() - 60000, hasActivity: true });
  assert.equal(h.engine.getState().stale, false, "partially aged activity is still fresh");
  assert.equal(h.engine.getState().rafActive, true);
  assert.equal(h.timerStats.arms, 1, "partially aged activity arms stale timer once");
  assert.equal(h.timerStats.delays[0], 30000, "stale timer delay matches remaining window");
});

test("unchanged DOM syncs after stale timeout do not re-arm the stale timer", async t => {
  const h = createHarness();
  h.engine.destroy();
  const eng = h.WsConvMotion.createEngine({
    doc: h.doc, win: h.win, root: h.root, convo: h.convo, grid: h.grid, convlive: h.convlive, canvas: h.canvas,
    staleMs: 30,
    now: () => h.now()
  });
  t.after(() => eng.destroy());

  const initialAt = h.now();
  eng.observe({ phase: "thinking", done: 0, failed: 0, turn: "s3", activityAt: initialAt, hasActivity: true });
  assert.equal(eng.getState().stale, false);
  assert.equal(eng.getState().rafActive, true);
  assert.equal(h.timerStats.arms, 1, "initial active observation arms stale timer once");

  await new Promise(r => setTimeout(r, 60));
  assert.equal(eng.getState().stale, true, "thinking state became stale after timeout");
  assert.equal(eng.getState().rafActive, false);
  assert.equal(h.rafQueue.size, 0);

  // Repeated unchanged syncs while already stale must NOT re-arm the stale timer.
  for (let i = 0; i < 4; i++) {
    eng.observe({ phase: "thinking", done: 0, failed: 0, turn: "s3", activityAt: initialAt, hasActivity: true });
  }
  assert.equal(h.timerStats.arms, 1, "unchanged thinking syncs must not re-arm stale timer");
  assert.equal(eng.getState().stale, true);
  assert.equal(eng.getState().rafActive, false);

  // Same guarantee for tool phase after stale timeout.
  h.advanceNow(100);
  const toolAt = h.now();
  eng.observe({ phase: "tool", tool: "ws/git.diff", done: 1, failed: 0, turn: "s3", activityAt: toolAt, hasActivity: true });
  assert.equal(eng.getState().stale, false, "fresh tool update exits stale");
  assert.equal(eng.getState().rafActive, true);
  assert.equal(h.timerStats.arms, 2, "fresh tool update arms stale timer once");

  await new Promise(r => setTimeout(r, 60));
  assert.equal(eng.getState().stale, true, "tool state became stale after timeout");
  assert.equal(eng.getState().rafActive, false);
  for (let i = 0; i < 4; i++) {
    eng.observe({ phase: "tool", tool: "ws/git.diff", done: 1, failed: 0, turn: "s3", activityAt: toolAt, hasActivity: true });
  }
  assert.equal(h.timerStats.arms, 2, "unchanged tool syncs must not re-arm stale timer");
  assert.equal(eng.getState().stale, true);
  assert.equal(eng.getState().rafActive, false);
});
