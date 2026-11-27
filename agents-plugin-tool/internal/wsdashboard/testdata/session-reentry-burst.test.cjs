"use strict";

// Cached-session re-entry hotfix regressions:
// 1. dashboard-audit-rows.js coalesces event/observer triggers via rAF/timer.
// 2. dashboard-whiteboard-only.js deduplicates merge + observer folds, skips
//    clone/audit mutations, and avoids re-cloning unchanged turns.
// 3. dashboard-companion-relay.js defers reveal reconcile(true) off the reveal
//    turn and cancels pending reveals on hide/dispose.
// 4. dashboard-conv-motion.js suppresses catch-up flights, pop-in layout reads,
//    and badge reflows during the post-reveal settle window.
// 5. dashboard-frames.js & dashboard-board.js snap wbUnmounted/returning
//    doc-frames without WAAPI growth and batch/skip boardFlash reflows.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

function readAsset(name) {
  return fs.readFileSync(path.join(__dirname, "..", "assets", name), "utf8");
}

function extractFunc(source, name) {
  const m = source.match(
    new RegExp(
      "\\n  function " +
        name +
        "\\([^\\n]*\\)\\s*\\{[^\\n]*\\}\\n|\\n  function " +
        name +
        "\\([\\s\\S]*?\\n  \\}\\n"
    )
  );
  assert.ok(m, "function not found: " + name);
  return m[0];
}

test("dashboard-audit-rows coalesces reveal bursts into one scheduled reconcile pass", async () => {
  const src = readAsset("dashboard-audit-rows.js");
  const listeners = {};
  const observerCbs = [];
  const rafs = new Map();
  let nextRaf = 1;
  let reconcileScans = 0;

  const entryNode = {
    nodeType: 1,
    nextElementSibling: null,
    classList: { contains: () => false },
    hasAttribute(k) {
      return k === "data-entry";
    },
    getAttribute(k) {
      if (k === "data-entry") return "e1";
      return null;
    },
    matches(sel) {
      return sel.includes("[data-entry]");
    },
    querySelectorAll() {
      return [];
    },
    querySelector() {
      return null;
    },
  };

  const entriesEl = {
    firstElementChild: entryNode,
    children: [entryNode],
    querySelectorAll() {
      return [];
    },
    querySelector() {
      return null;
    },
  };

  const liveEl = {
    querySelector(sel) {
      if (sel.includes("[data-retry-attempt]")) return { getAttribute: () => "1" };
      if (sel.includes("[data-audit-owner]")) return {};
      return null;
    },
  };

  const convoEl = {
    querySelectorAll(sel) {
      if (sel === "[data-au-row-key]") reconcileScans++;
      return [];
    },
  };

  const doc = {
    hidden: false,
    addEventListener(type, fn) {
      (listeners[type] ||= []).push(fn);
    },
    getElementById(id) {
      if (id === "r-convlive") return liveEl;
      if (id === "r-entries") return { querySelector: () => entriesEl };
      if (id === "convo") return convoEl;
      return null;
    },
    querySelector(sel) {
      if (sel.includes(".centries")) return entriesEl;
      return null;
    },
    querySelectorAll() {
      return [];
    },
  };

  const ctx = vm.createContext({
    doc,
    root: { getAttribute: () => "lead-reentry" },
    $: (id) => (id === "convo" ? convoEl : null),
    window: { WsAudit: {} },
    rid: "lead-reentry",
    shHidden: false,
    stick: false,
    Map,
    Set,
    WeakMap,
    Array,
    Object,
    Promise,
    requestAnimationFrame(fn) {
      const id = nextRaf++;
      rafs.set(id, fn);
      return id;
    },
    cancelAnimationFrame(id) {
      rafs.delete(id);
    },
    MutationObserver: class {
      constructor(cb) {
        observerCbs.push(cb);
      }
      observe() {}
      takeRecords() {
        return [];
      }
    },
    fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve({ events: [] }) }),
  });

  vm.runInContext(src, ctx);

  // Initial init ran once synchronously.
  const initScans = reconcileScans;
  assert.equal(initScans, 1);
  assert.equal(rafs.size, 0);

  // Multiple burst events in the same turn queue only one rAF reconcile.
  (listeners["ws-entries-merged"] || []).forEach((fn) => fn());
  (listeners["ws-approval-rows-changed"] || []).forEach((fn) => fn());
  observerCbs.forEach((cb) => cb());

  assert.equal(rafs.size, 1, "expected burst triggers to share one rAF");
  assert.equal(reconcileScans, initScans, "burst should not reconcile synchronously");

  const scheduled = [...rafs.values()];
  rafs.clear();
  scheduled.forEach((fn) => fn());
  assert.equal(reconcileScans, initScans + 1, "flushing rAF performs one reconcile");

  // Direct WsAuditRows.reconcile() runs synchronously and cancels queued work.
  (listeners["ws-approval-rows-changed"] || []).forEach((fn) => fn());
  assert.equal(rafs.size, 1);
  ctx.window.WsAuditRows.reconcile();
  assert.equal(rafs.size, 0, "synchronous reconcile clears pending scheduled tick");
  assert.equal(reconcileScans, initScans + 2);
});

test("dashboard-whiteboard-only deduplicates merge+observer, skips non-fold mutations, and keeps unchanged turns", () => {
  const src = readAsset("dashboard-whiteboard-only.js");

  class El {
    constructor(tag, attrs = {}, children = []) {
      this.nodeType = 1;
      this.tag = tag;
      this.attrs = { ...attrs };
      this.children = [];
      this.parentNode = null;
      this.listeners = {};
      this.cloneCount = 0;
      children.forEach((c) => this.appendChild(c));
    }
    get hidden() {
      return "hidden" in this.attrs;
    }
    set hidden(v) {
      if (v) this.attrs.hidden = "";
      else delete this.attrs.hidden;
    }
    getAttribute(k) {
      return k in this.attrs ? this.attrs[k] : null;
    }
    setAttribute(k, v) {
      this.attrs[k] = String(v);
    }
    removeAttribute(k) {
      delete this.attrs[k];
    }
    hasAttribute(k) {
      return k in this.attrs;
    }
    toggleAttribute(k, on) {
      if (on) this.attrs[k] = "";
      else delete this.attrs[k];
    }
    appendChild(c) {
      c.parentNode = this;
      this.children.push(c);
      return c;
    }
    remove() {
      if (this.parentNode) {
        this.parentNode.children = this.parentNode.children.filter((x) => x !== this);
      }
      this.parentNode = null;
    }
    addEventListener(t, f) {
      (this.listeners[t] ||= []).push(f);
    }
    cloneNode(deep) {
      this.cloneCount++;
      return new El(
        this.tag,
        this.attrs,
        deep ? this.children.map((c) => c.cloneNode(true)) : []
      );
    }
    matches(sel) {
      const classes = new Set((this.attrs.class || "").split(/\s+/).filter(Boolean));
      return sel.split(",").some((part) => {
        const tokens = [...part.trim().matchAll(/\.([\w-]+)|\[([\w-]+)(?:="([^"]*)")?\]/g)];
        if (!tokens.length) return false;
        for (const m of tokens) {
          if (m[1] && !classes.has(m[1])) return false;
          if (m[2] && !(m[2] in this.attrs)) return false;
          if (m[3] !== undefined && this.attrs[m[2]] !== m[3]) return false;
        }
        return true;
      });
    }
    walk(out = []) {
      this.children.forEach((c) => {
        out.push(c);
        c.walk(out);
      });
      return out;
    }
    querySelectorAll(sel) {
      return this.walk().filter((n) => n.matches(sel));
    }
    querySelector(sel) {
      return this.querySelectorAll(sel)[0] || null;
    }
  }

  const reply1 = new El("article", { class: "msg fromlead", "data-entry": "e1" });
  const drawer1 = new El("div", { "data-conv-turn-drawer": "" });
  const chip1 = new El("span", { "data-conv-turn-folded": "", hidden: "" });
  const turn1 = new El("div", { "data-conv-turn": "", "data-replies": "e1" }, [drawer1, chip1]);
  const box = new El("div", { class: "centries" }, [reply1, turn1]);
  const btn = new El("button", { id: "wbonly", "aria-pressed": "true" });
  const root = new El("html");
  let taken = 0;
  let observerCb = null;
  const rafs = [];

  const ctx = vm.createContext({
    rid: "sess-wb",
    root,
    Map,
    Set,
    WeakMap,
    Array,
    localStorage: {
      getItem: () => "on",
      setItem() {},
      removeItem() {},
    },
    $: (id) => (id === "wbonly" ? btn : null),
    entriesBox: () => box,
    scrollState: () => ({}),
    restoreScroll() {},
    queuePinConvo() {},
    requestAnimationFrame(fn) {
      rafs.push(fn);
      return rafs.length;
    },
    cancelAnimationFrame() {},
    MutationObserver: class {
      constructor(cb) {
        observerCb = cb;
      }
      observe() {}
      takeRecords() {
        taken++;
        return [];
      }
    },
  });

  vm.runInContext(src + "\nthis.__wbFoldSync = wbFoldSync;", ctx);

  // Initial fold ran before observer creation.
  assert.equal(reply1.cloneCount, 1);
  assert.equal(taken, 0);

  // Audit row and self-clone mutations should NOT schedule a fold sync.
  observerCb([
    { addedNodes: [new El("div", { class: "au-row" })], removedNodes: [] },
    { addedNodes: [new El("article", { "data-wb-clone": "1" })], removedNodes: [] },
  ]);
  assert.equal(rafs.length, 0, "audit and clone mutations must be ignored");

  // Re-running wbFoldSync with unchanged turn1 reuses existing clone and drains takeRecords.
  ctx.__wbFoldSync();
  assert.equal(reply1.cloneCount, 1, "unchanged turn must not re-clone reply1");
  assert.equal(taken, 1);

  // Adding a second turn schedules via rAF when MutationObserver sees it and leaves turn1 untouched.
  const reply2 = new El("article", { class: "msg fromlead", "data-entry": "e2" });
  const drawer2 = new El("div", { "data-conv-turn-drawer": "" });
  const chip2 = new El("span", { "data-conv-turn-folded": "", hidden: "" });
  const turn2 = new El("div", { "data-conv-turn": "", "data-replies": "e2" }, [drawer2, chip2]);
  box.appendChild(reply2);
  box.appendChild(turn2);

  observerCb([{ addedNodes: [reply2, turn2], removedNodes: [] }]);
  assert.equal(rafs.length, 1, "conversation mutation schedules rAF fold");
  rafs.shift()();
  assert.equal(reply1.cloneCount, 1, "existing unchanged turn stays intact");
  assert.equal(reply2.cloneCount, 1, "newly added turn folds once");
  assert.equal(taken, 2);
});

test("dashboard-companion-relay defers reveal reconcile off visible() and cancels on hide or dispose", () => {
  const src = readAsset("dashboard-companion-relay.js");

  function setup(useRaf) {
    let hidden = true;
    const listeners = {};
    const rafs = new Map();
    const timers = new Map();
    let nextId = 1;
    let collects = 0;

    const row = {
      dataset: { entry: "e1" },
      hasAttribute: () => false,
      matches: () => false,
      querySelector: () => null,
    };

    const doc = {
      body: { classList: { contains: () => false } },
      querySelector: () => null,
      addEventListener(type, fn) {
        (listeners[type] ||= []).push(fn);
      },
    };

    const ctxObj = {
      doc,
      window: {
        WsShell: { companionContext: () => ({ landmarks: {} }) },
        addEventListener() {},
      },
      AbortController,
      MutationObserver: class {
        observe() {}
        disconnect() {}
      },
      ResizeObserver: class {
        observe() {}
        disconnect() {}
      },
      performance: { now: () => 1000 },
      Date,
      setTimeout(fn) {
        const id = nextId++;
        timers.set(id, fn);
        return id;
      },
      clearTimeout(id) {
        timers.delete(id);
      },
      rid: "lead",
      lead: {},
      boardLatest: null,
      TL_ASK: "ask",
      companionStreamReady: true,
      q: () => [],
      boardQ: () => [],
      region: () => ({}),
      hudReadData: () => ({ roots: [], completed: 0 }),
      entriesBox() {
        collects++;
        return { children: [row] };
      },
      $: () => null,
      pageHidden: () => hidden,
      showRegions() {},
      shPost() {},
      innerHeight: 1000,
      innerWidth: 1000,
    };
    if (useRaf) {
      ctxObj.requestAnimationFrame = (fn) => {
        const id = nextId++;
        rafs.set(id, fn);
        return id;
      };
      ctxObj.cancelAnimationFrame = (id) => {
        rafs.delete(id);
      };
    }

    const ctx = vm.createContext(ctxObj);
    vm.runInContext(src + "\nthis.relay = startCompanionRelay();", ctx);
    return {
      ctx,
      rafs,
      timers,
      getCollects: () => collects,
      setHidden(v) {
        hidden = v;
        (listeners.visibilitychange || []).forEach((fn) => fn());
      },
    };
  }

  // With rAF: reveal does NOT collect/reconcile synchronously; rAF runs reconcile(true).
  const a = setup(true);
  a.setHidden(false);
  assert.equal(a.getCollects(), 0, "reveal must not synchronously run reconcile/collect");
  assert.equal(a.rafs.size, 1, "reveal should schedule one rAF");
  assert.equal(a.timers.size, 0, "reveal should prefer rAF over setTimeout");
  [...a.rafs.values()].forEach((fn) => fn());
  assert.equal(a.getCollects(), 1, "rAF callback runs reconcile(true)");

  // Re-hiding before rAF cancels the scheduled reveal.
  const b = setup(true);
  b.setHidden(false);
  assert.equal(b.rafs.size, 1);
  b.setHidden(true);
  assert.equal(b.rafs.size, 0, "hiding must cancel pending reveal rAF");
  assert.equal(b.getCollects(), 0);

  // dispose() cancels fallback setTimeout reveal too.
  const c = setup(false);
  c.setHidden(false);
  assert.equal(c.timers.size, 1);
  c.ctx.relay.dispose();
  assert.equal(c.timers.size, 0, "dispose must cancel pending reveal timer");
  assert.equal(c.getCollects(), 0);
});

test("dashboard-conv-motion suppresses catch-up bursts during return settle window and resumes after", () => {
  const src = readAsset("dashboard-conv-motion.js");
  let now = 10000;
  let rafId = 1;
  const rafs = new Map();
  let rectReads = 0;

  const canvas = {
    width: 400,
    height: 300,
    style: {},
    hidden: false,
    getBoundingClientRect() {
      rectReads++;
      return { left: 0, top: 0, width: 400, height: 300 };
    },
    getContext: () => ({ clearRect() {}, save() {}, restore() {}, translate() {}, scale() {}, setTransform() {} }),
  };

  const convoEl = {
    querySelectorAll: () => [],
    querySelector: () => null,
    getBoundingClientRect() {
      rectReads++;
      return { left: 0, top: 0, width: 400, height: 300 };
    },
  };

  const doc = {
    hidden: false,
    documentElement: {},
    getElementById: (id) => (id === "convo" ? convoEl : null),
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => null,
  };

  const ctx = vm.createContext({
    doc,
    window: { addEventListener() {}, removeEventListener() {} },
    Map,
    Set,
    WeakMap,
    Math,
    Date,
    setTimeout: () => 1,
    clearTimeout() {},
    ResizeObserver: undefined,
  });

  vm.runInContext(src, ctx);
  const engine = ctx.window.WsConvMotion.createEngine({
    doc,
    convo: convoEl,
    canvas,
    now: () => now,
    perfNow: () => now,
    hidden: false,
    enabled: true,
    returnSettleMs: 1500,
    requestAnimationFrame(fn) {
      const id = rafId++;
      rafs.set(id, fn);
      return id;
    },
    cancelAnimationFrame(id) {
      rafs.delete(id);
    },
  });

  // Initial seed while visible.
  engine.observe({ turn: "t1", phase: "tool", tool: "read_file", done: 1, failed: 0, exactTokens: 100, hasActivity: true });

  // Hide and re-reveal -> clears particles, opens settle window, still ensures rAF loop.
  engine.setHidden(true);
  now += 5000;
  engine.setHidden(false);
  assert.equal(rafs.size, 1, "ensureLoop still starts on reveal");

  const rectsBefore = rectReads;
  // Re-entry catch-up deltas during 1500ms settle window must NOT spawn flights or synchronous geometry reads.
  engine.observe({ turn: "t1", phase: "tool", tool: "read_file", done: 3, failed: 0, exactTokens: 300, hasActivity: true });
  engine.observe({ turn: "t1", phase: "tool", tool: "edit_file", done: 6, failed: 1, exactTokens: 600, hasActivity: true });
  assert.equal(engine.getState().flights, 0, "catch-up deltas must not launch flights during settle");
  assert.equal(rectReads, rectsBefore, "catch-up tool switch must not force synchronous geometry reads");

  // Once returnSettleMs expires, normal live delta launches flights and syncs geometry cleanly.
  now += 1600;
  engine.observe({ turn: "t1", phase: "tool", tool: "run_command", done: 7, failed: 1, exactTokens: 700, hasActivity: true });
  assert.equal(engine.getState().flights, 1, "post-settle live delta launches normal flight");
  assert.ok(rectReads > rectsBefore, "post-settle tool change measures geometry normally");
});

test("dashboard-frames and dashboard-board snap unmounted/returning frames and gate boardFlash", () => {
  const framesSrc = readAsset("dashboard-frames.js");
  const boardSrc = readAsset("dashboard-board.js");

  class Element {
    constructor(tag, attrs = {}) {
      this.tagName = tag.toUpperCase();
      this.attrs = { ...attrs };
      this.style = {};
      this.parentNode = null;
      this.children = [];
      this.isConnected = true;
      this.animateCalls = [];
      this.offsetReads = 0;
      this.classList = {
        set: new Set((attrs.class || "").split(/\s+/).filter(Boolean)),
        contains: (c) => this.classList.set.has(c),
        add: (c) => this.classList.set.add(c),
        remove: (c) => this.classList.set.delete(c),
      };
    }
    get attributes() {
      return Object.entries(this.attrs).map(([name, value]) => ({ name, value }));
    }
    hasAttribute(k) {
      return k in this.attrs;
    }
    getAttribute(k) {
      return k in this.attrs ? this.attrs[k] : null;
    }
    setAttribute(k, v) {
      this.attrs[k] = String(v);
    }
    get offsetWidth() {
      this.offsetReads++;
      return 120;
    }
    animate(kf, opts) {
      this.animateCalls.push({ kf, opts });
    }
  }

  let topicCards = [];
  const doc = {
    createElement: (tag) => new Element(tag),
  };

  const ctx = vm.createContext({
    Map,
    Set,
    Array,
    Math,
    parseFloat,
    doc,
    shHidden: false,
    shStaged: false,
    boardReturnTimer: 1,
    frameSizes: new Map(),
    frameSizeFrame: 0,
    window: {},
    requestAnimationFrame() {
      return 1;
    },
    clearTimeout() {},
    setTimeout: () => 1,
    boardKeep: () => ({ scroller: () => ({ wbScrollOwner: false }), h: { apply() {} } }),
    postTheme() {},
    boardBoxes: () => [{}],
    q: () => topicCards,
    topicKey: (n) => n.getAttribute("data-topic"),
    boardExposed: () => true,
  });

  vm.runInContext(
    extractFunc(framesSrc, "mountFrame") +
      extractFunc(framesSrc, "applyFrameHeights") +
      extractFunc(boardSrc, "boardFlash"),
    ctx
  );

  // 1. mountFrame forwards p.wbUnmounted while keeping f.wbGrow intact.
  const parent = {
    replaceChild(n, old) {
      n.parentNode = parent;
      old.parentNode = null;
    },
  };
  const p = new Element("div", { "data-srcdoc": "<p>ok</p>" });
  p.parentNode = parent;
  p.wbUnmounted = true;
  p.wbGrow = true;
  const f = ctx.mountFrame(p);
  assert.equal(f.wbUnmounted, true);
  assert.equal(f.wbGrow, true);

  // 2. applyFrameHeights skips animate during wbUnmounted and clears flag.
  ctx.boardReturnTimer = 0;
  f.style.height = "0px";
  ctx.frameSizes.set(f, 320);
  ctx.applyFrameHeights();
  assert.equal(f.style.height, "320px");
  assert.equal(f.animateCalls.length, 0, "wbUnmounted frame must snap height without WAAPI animation");
  assert.equal(f.wbUnmounted, false);

  // 3. applyFrameHeights skips animate during boardReturnTimer even for regular wbGrow frame.
  const fRet = new Element("iframe");
  fRet.style.height = "100px";
  fRet.wbGrow = true;
  ctx.boardReturnTimer = 1;
  ctx.frameSizes.set(fRet, 260);
  ctx.applyFrameHeights();
  assert.equal(fRet.style.height, "260px");
  assert.equal(fRet.animateCalls.length, 0, "boardReturnTimer must suppress WAAPI height grow");

  // 4. Normal post-return wbGrow still animates.
  const fLive = new Element("iframe");
  fLive.style.height = "100px";
  fLive.wbGrow = true;
  ctx.boardReturnTimer = 0;
  ctx.frameSizes.set(fLive, 260);
  ctx.applyFrameHeights();
  assert.equal(fLive.animateCalls.length, 1, "live wbGrow frame outside return still animates");

  // 5. boardFlash is suppressed during boardReturnTimer, and batches reflows cleanly after.
  const t1 = new Element("section", { "data-topic": "a", class: "board-fresh" });
  const t2 = new Element("section", { "data-topic": "b", class: "board-fresh" });
  topicCards = [t1, t2];

  ctx.boardReturnTimer = 1;
  ctx.boardFlash(["a", "b"], false);
  assert.equal(t1.offsetReads + t2.offsetReads, 0, "boardFlash during return must not force reflow");

  ctx.boardReturnTimer = 0;
  ctx.boardFlash(["a", "b"], false);
  assert.equal(t1.offsetReads + t2.offsetReads, 1, "batched boardFlash performs one single offsetWidth flush");
  assert.equal(t1.classList.contains("board-fresh"), true);
  assert.equal(t2.classList.contains("board-fresh"), true);
});
