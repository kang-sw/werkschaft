"use strict";

// Whiteboard-only mode client: capability gating, persistence, the send flag,
// and the incremental fold of lead replies into turn drawers. Runs the real
// dashboard-whiteboard-only.js against a minimal DOM.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const src = fs.readFileSync(path.join(__dirname, "..", "assets", "dashboard-whiteboard-only.js"), "utf8");

class El {
  constructor(tag, attrs = {}, children = []) {
    this.nodeType = 1;
    this.tag = tag;
    this.attrs = { ...attrs };
    this.children = [];
    this.parentNode = null;
    this.listeners = {};
    children.forEach((c) => this.appendChild(c));
  }
  get hidden() { return "hidden" in this.attrs; }
  set hidden(v) { if (v) this.attrs.hidden = ""; else delete this.attrs.hidden; }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  removeAttribute(k) { delete this.attrs[k]; }
  hasAttribute(k) { return k in this.attrs; }
  toggleAttribute(k, on) { if (on) this.attrs[k] = ""; else delete this.attrs[k]; }
  appendChild(c) { c.parentNode = this; this.children.push(c); return c; }
  remove() { if (this.parentNode) this.parentNode.children = this.parentNode.children.filter((x) => x !== this); this.parentNode = null; }
  addEventListener(t, f) { (this.listeners[t] ||= []).push(f); }
  click() { (this.listeners.click || []).forEach((f) => f()); }
  cloneNode(deep) {
    const c = new El(this.tag, this.attrs);
    if (deep) this.children.forEach((k) => c.appendChild(k.cloneNode(true)));
    return c;
  }
  matches(sel) {
    const classes = new Set((this.attrs.class || "").split(/\s+/).filter(Boolean));
    for (const m of sel.matchAll(/\.([\w-]+)|\[([\w-]+)(?:="([^"]*)")?\]/g)) {
      if (m[1] && !classes.has(m[1])) return false;
      if (m[2] && !(m[2] in this.attrs)) return false;
      if (m[3] !== undefined && this.attrs[m[2]] !== m[3]) return false;
    }
    return true;
  }
  walk(out = []) { this.children.forEach((c) => { out.push(c); c.walk(out); }); return out; }
  querySelectorAll(sel) { return this.walk().filter((n) => n.matches(sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

function reply(key, text) {
  return new El("div", { class: "msg fromlead", "data-entry": key }, [new El("div", { class: "bub", text })]);
}
function summary(key, replies, plain) {
  const a = { class: "d-turn", "data-conv-turn": "1", "data-key": key, "data-replies": replies };
  if (plain) { a["data-conv-turn-plain"] = "1"; a.hidden = ""; }
  return new El("details", a, [new El("span", { "data-conv-turn-folded": "", hidden: "" }), new El("div", { "data-conv-turn-drawer": "" })]);
}

function load({ capable = true, stored = null } = {}) {
  const box = new El("div", { class: "centries" });
  const btn = capable ? new El("button", { id: "wbonly", "aria-pressed": "false" }) : null;
  const hint = capable ? new El("span", { id: "wbonly-hint", hidden: "" }) : null;
  const byId = { wbonly: btn, "wbonly-hint": hint };
  const store = new Map(stored ? [["ws.dashboard.whiteboardFirst.r1", stored]] : []);
  const calls = [];
  const ctx = {
    rid: "r1",
    root: new El("html"),
    $: (id) => byId[id] || null,
    entriesBox: () => box,
    scrollState: () => { calls.push("scrollState"); return []; },
    restoreScroll: () => calls.push("restoreScroll"),
    queuePinConvo: () => calls.push("pin"),
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, v),
      removeItem: (k) => store.delete(k),
    },
    MutationObserver: class { observe() {} },
  };
  vm.createContext(ctx);
  vm.runInContext(src + "\nthis.api = { wbOnlyOn, wbFoldSync };", ctx);
  return { ctx, box, btn, hint, store, calls };
}

function populate(box) {
  const tool = summary("turn-e2", "e3", false), plain = summary("turn-plain-e5", "e5", true);
  const turnline = new El("details", { class: "turnline" });
  const r1 = reply("e3", "tool reply"), r2 = reply("e5", "plain reply");
  box.appendChild(tool);
  box.appendChild(r1);
  box.appendChild(r2);
  box.appendChild(new El("div", { class: "turn-plain-host", "data-entry": "e6" }, [plain, turnline]));
  return { tool, plain, turnline, r1, r2 };
}

test("default OFF folds nothing and sends no flag", () => {
  const { ctx, box, btn } = load();
  const n = populate(box);
  assert.equal(ctx.api.wbOnlyOn(), false);
  assert.equal(btn.getAttribute("aria-pressed"), "false");
  assert.equal(n.r1.hasAttribute("data-wb-folded"), false);
  assert.equal(n.plain.hidden, true);
});

test("a session with no toggle is OFF even with a stored on", () => {
  const { ctx, box, store } = load({ capable: false, stored: "on" });
  const n = populate(box);
  ctx.api.wbFoldSync();
  assert.equal(ctx.api.wbOnlyOn(), false);
  assert.equal(n.r1.hasAttribute("data-wb-folded"), false);
  assert.equal(box.querySelectorAll("[data-wb-clone]").length, 0);
  assert.equal(store.get("ws.dashboard.whiteboardFirst.r1"), "on", "storage is left alone");
});

test("a stored on session starts ON and folds replies into drawers", () => {
  const { ctx, box, btn, hint } = load({ stored: "on" });
  const n = populate(box);
  ctx.api.wbFoldSync();
  assert.equal(ctx.api.wbOnlyOn(), true);
  assert.equal(btn.getAttribute("aria-pressed"), "true");
  assert.equal(hint.hidden, false);
  for (const [reply, turn, text] of [[n.r1, n.tool, "tool reply"], [n.r2, n.plain, "plain reply"]]) {
    assert.equal(reply.hasAttribute("data-wb-folded"), true, "original stays in place, hidden");
    assert.ok(box.querySelectorAll(".msg").includes(reply));
    const clones = turn.querySelector("[data-conv-turn-drawer]").children;
    assert.equal(clones.length, 1);
    assert.equal(clones[0].hasAttribute("data-wb-clone"), true);
    assert.equal(clones[0].hasAttribute("data-entry"), false, "clones never carry an entry key");
    assert.equal(clones[0].children[0].attrs.text, text);
  }
  assert.equal(n.plain.hidden, false, "the plain summary shows while ON");
  assert.equal(n.turnline.hasAttribute("data-wb-folded"), true, "its turnline hides");
  assert.equal(n.tool.querySelector("[data-conv-turn-folded]").hidden, false, "the tool turn names the folded replies");
});

test("toggling is persisted per session, scroll-locked, and reversible", () => {
  const { ctx, box, btn, store, calls } = load();
  const n = populate(box);
  btn.click();
  assert.equal(store.get("ws.dashboard.whiteboardFirst.r1"), "on");
  assert.deepEqual(calls.slice(0, 2), ["scrollState", "restoreScroll"]);
  assert.ok(calls.includes("pin"));
  assert.equal(n.r1.hasAttribute("data-wb-folded"), true);
  btn.click();
  assert.equal(store.has("ws.dashboard.whiteboardFirst.r1"), false);
  assert.equal(ctx.api.wbOnlyOn(), false);
  assert.equal(n.r1.hasAttribute("data-wb-folded"), false);
  assert.equal(n.r2.hasAttribute("data-wb-folded"), false);
  assert.equal(box.querySelectorAll("[data-wb-clone]").length, 0);
  assert.equal(n.plain.hidden, true);
  assert.equal(n.turnline.hasAttribute("data-wb-folded"), false);
});

test("a re-sync after a merge does not duplicate clones", () => {
  const { ctx, box } = load({ stored: "on" });
  const n = populate(box);
  ctx.api.wbFoldSync();
  ctx.api.wbFoldSync();
  assert.equal(n.tool.querySelector("[data-conv-turn-drawer]").children.length, 1);
});

test("the send path sets whiteboard_first only while ON", () => {
  const delivery = fs.readFileSync(path.join(__dirname, "..", "assets", "dashboard-composer-delivery.js"), "utf8");
  assert.match(delivery, /if \(typeof wbOnlyOn === "function" && wbOnlyOn\(\)\) body\.whiteboard_first = true;/);
});
