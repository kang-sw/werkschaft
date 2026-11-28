const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function makeFakeElement(tag) {
  const el = {
    tagName: String(tag).toUpperCase(),
    className: "",
    textContent: "",
    hidden: false,
    offsetWidth: 160,
    offsetHeight: 32,
    children: [],
    dataset: {},
    attrs: {},
    style: {
      setProperty(k, v) { this[k] = v; },
      getPropertyValue(k) { return this[k] || ""; }
    },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return Object.hasOwn(this.attrs, k) ? this.attrs[k] : null; },
    append(...items) { this.children.push(...items); },
    remove() { this.removed = true; }
  };
  return el;
}

function loadReflex(overrides = {}) {
  const timers = new Map();
  let nextTid = 1;
  const triggered = [];
  let wakes = 0;
  const win = {
    WsCompanion: {
      triggerGesture(name) { triggered.push(name); return true; },
      wakeStage() { wakes++; }
    }
  };
  const ctx = vm.createContext({
    window: win,
    setTimeout(fn, ms) {
      const id = nextTid++;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout(id) { timers.delete(id); },
    ...overrides
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-reflex.js"), "utf8"), ctx);
  return {
    reflex: win.WsCompanion.reflex,
    triggered,
    timers,
    get wakes() { return wakes; },
    flushAll() {
      for (const [id, entry] of [...timers.entries()]) {
        timers.delete(id);
        entry.fn();
      }
    }
  };
}

function loadQuip() {
  const win = { WsCompanion: {} };
  const ctx = vm.createContext({ window: win });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-quip.js"), "utf8"), ctx);
  return win.WsCompanion.quip;
}

test("0ms client reflex triggers phone_pickup immediately, arms 6s lease, wakes stage, and suppresses stale snapshots", () => {
  const h = loadReflex();
  assert.equal(h.reflex.isPhoneHoldActive(), false);
  assert.equal(h.reflex.onUserSend(), true);
  assert.deepEqual(h.triggered, ["phone_pickup"]);
  assert.equal(h.wakes, 1);
  assert.equal(h.reflex.isPhoneHoldActive(), true);
  assert.equal(h.reflex.debugState().reflexActive, true);
  assert.equal(h.timers.size, 1);
  assert.equal([...h.timers.values()][0].ms, 6000);

  // Stale buzz or laptop_ambient snapshots are suppressed during the reflex lease.
  assert.equal(h.reflex.suppressPhoneSnapshot({ state: "laptop_ambient", posture: "laptop" }), true);
  assert.equal(h.reflex.suppressPhoneSnapshot({ state: "phone_buzz", posture: "laptop" }), true);
  // Authoritative phone_active or phoneFaceDown snapshots are never suppressed.
  assert.equal(h.reflex.suppressPhoneSnapshot({ state: "phone_active", posture: "phone" }), false);
  assert.equal(h.reflex.suppressPhoneSnapshot({ state: "laptop_ambient", phoneFaceDown: true }), false);

  // Lease expiry safely transitions back to phone_putdown.
  h.flushAll();
  assert.equal(h.reflex.isPhoneHoldActive(), false);
  assert.deepEqual(h.triggered, ["phone_pickup", "phone_putdown"]);
});

test("authoritative Puppeteer phone_* command enforces monotonic seq and supersedes reflex lease cleanly", () => {
  const h = loadReflex();
  h.reflex.onUserSend();
  assert.equal(h.reflex.debugState().reflexActive, true);

  // Authoritative phone_typing with seq=5 clears the 6s reflex lease and arms a clamped Puppeteer hold.
  assert.equal(h.reflex.onCommand({ seq: 5, gesture: "phone_typing", ttl_ms: 99999 }), true);
  assert.equal(h.reflex.debugState().reflexActive, false);
  assert.equal(h.reflex.debugState().puppeteerHoldActive, true);
  assert.equal(h.reflex.isPhoneHoldActive(), true);
  assert.equal([...h.timers.values()][0].ms, 15000, "Puppeteer hold clamps to 15s max");

  // Out-of-order stale command is dropped.
  assert.equal(h.reflex.onCommand({ seq: 4, gesture: "phone_putdown" }), false);
  assert.equal(h.reflex.isPhoneHoldActive(), true);
  assert.equal(h.reflex.onCommand({ seq: 5, gesture: "phone_putdown" }), false);

  // Non-phone gesture with newer seq advances lastCommandSeq without dropping active hold.
  assert.equal(h.reflex.onCommand({ seq: 6, gesture: "nod", expression: "happy" }), true);
  assert.equal(h.reflex.isPhoneHoldActive(), true);

  // Authoritative phone_putdown releases the Puppeteer hold immediately.
  assert.equal(h.reflex.onCommand({ seq: 7, gesture: "phone_putdown" }), true);
  assert.equal(h.reflex.isPhoneHoldActive(), false);
  assert.equal(h.timers.size, 0);

  // Legacy commands without seq (T0 compatibility) are still accepted.
  assert.equal(h.reflex.onCommand({ gesture: "phone_pickup", ttl_ms: 200 }), true);
  assert.equal([...h.timers.values()][0].ms, 1000, "Puppeteer hold clamps to 1s min");
});

test("onBrainState releases active phone hold on brain_work_start, subagent_event, and brain_idle with monotonic seq", () => {
  const h = loadReflex();
  for (const [idx, kind] of ["brain_work_start", "subagent_event", "brain_idle"].entries()) {
    h.reflex.onUserSend();
    assert.equal(h.reflex.isPhoneHoldActive(), true);
    assert.equal(h.reflex.onBrainState({ kind, replyId: "brain-1", seq: idx + 1 }), true);
    assert.equal(h.reflex.isPhoneHoldActive(), false);
    assert.equal(h.triggered[h.triggered.length - 1], "phone_putdown");
  }

  // Stale brain-state seq is dropped.
  h.reflex.onUserSend();
  assert.equal(h.reflex.onBrainState({ kind: "brain_idle", seq: 2 }), false);
  assert.equal(h.reflex.isPhoneHoldActive(), true);

  // brain_reply / handoff do not tear down an active hold.
  assert.equal(h.reflex.onBrainState({ kind: "brain_reply", seq: 10 }), false);
  assert.equal(h.reflex.onBrainState({ kind: "handoff", replyId: "brain-2", seq: 11 }), false);
  assert.equal(h.reflex.isPhoneHoldActive(), true);

  // When not held, brain_work_start does not emit spurious phone_putdown.
  h.reflex.reset();
  const before = h.triggered.length;
  assert.equal(h.reflex.onBrainState({ kind: "brain_work_start", seq: 1 }), true);
  assert.equal(h.triggered.length, before);
});

test("colead PHONE_UNCONFIRMED_GRACE 4s revert is suppressed while C.reflex.isPhoneHoldActive() is true", () => {
  const src = fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-vrm-colead.js"), "utf8");
  assert.match(src, /phoneSnapHeld === false && \(phoneMode === "pickup" \|\| phoneMode === "typing"\) && !C\.reflex\?\.isPhoneHoldActive\?\.\(\)/);
});

test("stage quip enforces textContent-only XSS safety, 140-rune clamp, head-relative clamping, and steady-state zero layout reads", () => {
  const quipMod = loadQuip();
  const parent = makeFakeElement("div");
  let shellReads = 0;
  let offsetReads = 0;
  const doc = {
    createElement(tag) {
      const el = makeFakeElement(tag);
      let w = 180, h = 30;
      Object.defineProperty(el, "offsetWidth", {
        get() { offsetReads++; return w; },
        set(v) { w = v; }
      });
      Object.defineProperty(el, "offsetHeight", {
        get() { return h; },
        set(v) { h = v; }
      });
      return el;
    },
    getElementById(id) {
      if (id !== "shell-bottom") return null;
      return {
        hidden: false,
        getBoundingClientRect() { shellReads++; return { height: 26 }; }
      };
    }
  };

  const ctrl = quipMod.create(parent, doc);
  const payload = "<img src=x onerror=alert(1)>\t\n뭐라는 거야 " + "가".repeat(200);
  assert.equal(ctrl.set(payload), true);
  const el = ctrl.element();
  assert.equal(el.innerHTML, undefined, "never assigns innerHTML");
  assert.equal(Array.from(el.textContent).length, 140, "clamped to 140 Unicode code points");
  assert.ok(el.textContent.startsWith("<img src=x onerror=alert(1)> 뭐라는 거야 "));
  assert.equal(el.dataset.open, "1");
  assert.equal(el.hidden, false);

  // Position near top edge flips placement to below and computes clamped --quip-tail-x.
  const flipped = ctrl.position({
    vw: 1000, vh: 700, isVrm: true,
    projectedBounds: { viewportX: 10, viewportY: 12, width: 80, height: 140 }
  });
  assert.equal(flipped.placement, "below");
  assert.ok(flipped.left >= 8, "clamped inside left viewport padding");
  assert.ok(flipped.top >= 152, "placed below character when top clips");
  assert.equal(el.style["--quip-tail-x"], flipped.tailX + "px");

  // Position near bottom respects .shell-bottom reserve (26 + 6 = 32px + 8px pad).
  const normal = ctrl.position({
    vw: 1000, vh: 700, isVrm: true,
    projectedBounds: { viewportX: 400, viewportY: 660, width: 100, height: 120 }
  });
  assert.equal(normal.placement, "above");
  assert.ok(normal.top + normal.height <= 700 - 32 - 8, "never overlaps .shell-bottom");

  // Steady-state frames perform zero additional offsetWidth or getBoundingClientRect reads.
  const readsBefore = offsetReads;
  const shellBefore = shellReads;
  for (let i = 0; i < 10; i++) {
    ctrl.position({
      vw: 1000, vh: 700, isVrm: true,
      projectedBounds: { viewportX: 400 + i, viewportY: 400, width: 100, height: 120 }
    });
  }
  assert.equal(offsetReads, readsBefore, "no per-frame offsetWidth layout reads");
  assert.equal(shellReads, shellBefore, "no per-frame shell-bottom getBoundingClientRect reads");

  ctrl.hide();
  assert.equal(ctrl.isVisible(), false);
  assert.equal(el.dataset.open, "0");
  ctrl.dispose();
  assert.equal(el.removed, true);
});

test("stream and lifecycle wire companion-brain-state SSE to reflex and Messenger handoff", () => {
  const stream = fs.readFileSync(path.join(__dirname, "assets", "dashboard-stream.js"), "utf8");
  const lifecycle = fs.readFileSync(path.join(__dirname, "assets", "dashboard-lifecycle.js"), "utf8");
  assert.match(stream, /"companion-brain-state":\s*onCompanionBrainState/);
  assert.match(stream, /new CustomEvent\("ws-companion-brain-state"/);
  assert.match(lifecycle, /addEventListener\("ws-companion-brain-state"/);
  assert.match(lifecycle, /WsCompanion\?\.reflex\?\.onBrainState\?\.\(detail\)/);
  assert.match(lifecycle, /WsCompanionMessenger\?\.onBrainChanged\?\.\(detail\)/);
});

test("all 11 locales carry updated Puppeteer/Full Lead tier labels and Messenger Brain keys in strict parity", () => {
  const dir = path.join(__dirname, "..", "wsi18n", "tables");
  const files = fs.readdirSync(dir).filter(f => f.endsWith(".json")).sort();
  assert.equal(files.length, 11);
  const en = JSON.parse(fs.readFileSync(path.join(dir, "en.json"), "utf8"));
  const enKeys = Object.keys(en).sort();
  const required = [
    "js.companion.messenger.open_full_tab",
    "js.companion.messenger.brain_retry",
    "js.companion.messenger.brain_ready",
    "js.companion.messenger.brain_unavailable",
    "js.settings.companion.agents.body_hint",
    "js.settings.companion.agents.work_hint",
    "js.settings.key.dashboard.companion_body_tier.label",
    "js.settings.key.dashboard.companion_body_tier.description",
    "js.settings.key.dashboard.companion_work_tier.label",
    "js.settings.key.dashboard.companion_work_tier.description"
  ];
  assert.match(en["js.settings.key.dashboard.companion_body_tier.label"], /Puppeteer/i);
  assert.match(en["js.settings.key.dashboard.companion_work_tier.label"], /Full Lead/i);
  for (const file of files) {
    const tbl = JSON.parse(fs.readFileSync(path.join(dir, file), "utf8"));
    assert.deepEqual(Object.keys(tbl).sort(), enKeys, file + " key parity with en.json");
    for (const k of required) {
      assert.ok(typeof tbl[k] === "string" && tbl[k].trim().length > 0, `${file} missing ${k}`);
    }
  }
});
