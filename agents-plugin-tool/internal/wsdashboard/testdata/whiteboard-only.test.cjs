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

// writes counts every DOM mutation the script performs, so a test can assert a
// no-change sync is write-free.
const writes = { n: 0 };

class El {
  constructor(tag, attrs = {}, children = []) {
    this.nodeType = 1;
    this.tag = tag;
    this.attrs = { ...attrs };
    this.children = [];
    this.parentNode = null;
    this.listeners = {};
    this.text = "";
    children.forEach((c) => this.appendChild(c));
  }
  get hidden() { return "hidden" in this.attrs; }
  set hidden(v) { writes.n++; if (v) this.attrs.hidden = ""; else delete this.attrs.hidden; }
  get textContent() { return this.text; }
  set textContent(v) { writes.n++; this.text = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  setAttribute(k, v) { writes.n++; this.attrs[k] = String(v); }
  removeAttribute(k) { writes.n++; delete this.attrs[k]; }
  hasAttribute(k) { return k in this.attrs; }
  toggleAttribute(k, on) { if (on) this.attrs[k] = ""; else delete this.attrs[k]; }
  appendChild(c) { writes.n++; if (c.parentNode) c.parentNode.children = c.parentNode.children.filter((x) => x !== c); c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) {
    writes.n++;
    if (c.parentNode) c.parentNode.children = c.parentNode.children.filter((x) => x !== c);
    c.parentNode = this;
    const at = ref ? this.children.indexOf(ref) : -1;
    if (at < 0) this.children.push(c); else this.children.splice(at, 0, c);
    return c;
  }
  remove() { writes.n++; if (this.parentNode) this.parentNode.children = this.parentNode.children.filter((x) => x !== this); this.parentNode = null; }
  addEventListener(t, f) { (this.listeners[t] ||= []).push(f); }
  click() { (this.listeners.click || []).forEach((f) => f()); }
  cloneNode(deep) {
    const c = new El(this.tag, this.attrs);
    if (deep) this.children.forEach((k) => c.appendChild(k.cloneNode(true)));
    return c;
  }
  matches(sel) {
    if (sel.includes(",")) return sel.split(",").some((s) => this.matches(s.trim()));
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
  const byId = { wbonly: btn };
  const store = new Map(stored ? [["ws.dashboard.whiteboardFirst.r1", stored]] : []);
  const calls = [];
  const rafs = [];
  const observer = { cb: null, taken: 0 };
  const ctx = {
    rid: "r1",
    root: new El("html"),
    $: (id) => byId[id] || null,
    entriesBox: () => box,
    doc: { createElement: (tag) => new El(tag) },
    wsT: (k) => k,
    wsTn: (k, n) => k + (n === 1 ? ".one" : ".other") + ":" + n,
    scrollState: () => { calls.push("scrollState"); return []; },
    restoreScroll: () => calls.push("restoreScroll"),
    queuePinConvo: () => calls.push("pin"),
    requestAnimationFrame: (fn) => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: () => {},
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, v),
      removeItem: (k) => store.delete(k),
    },
    MutationObserver: class {
      constructor(cb) { observer.cb = cb; }
      observe() {}
      takeRecords() { observer.taken++; return []; }
    },
  };
  vm.createContext(ctx);
  vm.runInContext(src + "\nthis.api = { wbOnlyOn, wbFoldSync, wbNeedsFoldForMutations, wbRevealClustered };", ctx);
  return { ctx, box, btn, store, calls, rafs, observer };
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
  for (const stored of ["on", "1"]) {
    const { ctx, box, btn } = load({ stored });
    const n = populate(box);
    ctx.api.wbFoldSync();
    assert.equal(ctx.api.wbOnlyOn(), true, "stored=" + stored + " starts ON");
    assert.equal(btn.getAttribute("aria-pressed"), "true");
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
  }
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

// --- folded-turn clusters ---------------------------------------------------

const hm = (ms) => new Date(ms).toTimeString().slice(0, 5);
const T0 = new Date(2026, 10, 28, 17, 3).getTime();
const MIN = 60000;

// A completed tool turn row as the server renders it (anchor + stats attrs).
function foldedTurn(n, { ts = T0 + n * MIN, calls = 2, mails = 0, tok = 0, est = false, failed = 0, live = false, replies = "" } = {}) {
  const a = {
    class: "toolsline d-turn" + (live ? " live d-turn-live" : ""), "data-conv-turn": "1", "data-entry": "e" + n,
    "data-key": "turn-e" + n, "data-turn-key": "t" + n, "data-turn-rev": "r" + n,
    "data-turn-calls": String(calls), "data-turn-mails": String(mails), "data-turn-tok": String(tok), "data-turn-ts": String(ts),
  };
  if (replies) a["data-replies"] = replies;
  if (est) a["data-turn-tok-est"] = "1";
  if (failed) a["data-turn-failed"] = String(failed);
  return new El("details", a, [new El("div", { "data-conv-turn-drawer": "" })]);
}
function plainTurn(n, { ts = T0 + n * MIN } = {}) {
  const sum = summary("turn-plain-e" + n, "r" + n, true);
  sum.attrs["data-turn-ts"] = String(ts);
  sum.attrs["data-turn-tok"] = "100";
  sum.attrs["data-turn-calls"] = "0";
  sum.attrs["data-turn-mails"] = "0";
  const host = new El("div", { class: "turn-plain-host", "data-entry": "h" + n }, [sum, new El("details", { class: "turnline" })]);
  return { host, reply: reply("r" + n, "plain " + n), sum };
}
function sysMail(n, { ts = T0 + n * MIN } = {}) {
  return new El("div", { class: "mailline turn-trigger recv nobody", "data-entry": "m" + n, "data-mail-system": "1", "data-mail-ts": String(ts) });
}
const headers = (box) => box.children.filter((c) => c.hasAttribute("data-wb-cluster-key"));
const state = (el) => (el.hasAttribute("data-wb-clustered") ? "clustered" : el.hasAttribute("data-wb-cluster-open") ? "open" : "plain");

test("18 consecutive folded turns collapse into one range header with aggregated counts", () => {
  const { ctx, box } = load({ stored: "on" });
  const turns = [];
  for (let i = 0; i < 18; i++) turns.push(box.appendChild(foldedTurn(i, { calls: i < 13 ? 1 : 0, mails: i < 3 ? 2 : 0, tok: 1000, ts: T0 + i * 5 * MIN })));
  ctx.api.wbFoldSync();
  const hs = headers(box);
  assert.equal(hs.length, 1);
  const h = hs[0];
  assert.equal(h.getAttribute("data-wb-cluster-key"), "t0");
  assert.equal(h.getAttribute("aria-expanded"), "false");
  assert.equal(box.children[0], h, "the header sits immediately before the first member");
  assert.equal(h.wbCount.textContent, "js.conv.cluster.turns.other:18");
  assert.equal(h.wbCalls.textContent, "js.conv.cluster.tools.other:13");
  assert.equal(h.wbMails.textContent, "js.conv.cluster.mails.other:6");
  assert.equal(h.wbTok.textContent, "+18.0k tok");
  assert.equal(h.wbTime.textContent, hm(T0) + ".." + hm(T0 + 17 * 5 * MIN));
  assert.equal(box.children.length, 19, "members are never reparented or cloned");
  turns.forEach((t, i) => {
    assert.equal(box.children[i + 1], t);
    assert.equal(state(t), "clustered");
  });
});

test("identical first and last times show one HH:MM, zero chips stay hidden", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(foldedTurn(1, { ts: T0, calls: 0 }));
  box.appendChild(foldedTurn(2, { ts: T0 + 20000, calls: 0 }));
  ctx.api.wbFoldSync();
  const h = headers(box)[0];
  assert.equal(h.wbTime.textContent, hm(T0), "no .. when the minutes are identical");
  assert.equal(h.wbCalls.hasAttribute("hidden"), true);
  assert.equal(h.wbMails.hasAttribute("hidden"), true);
  assert.equal(h.wbTok.hasAttribute("hidden"), true);
});

test("an estimated token sum is marked with ~", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(foldedTurn(1, { tok: 500, est: true }));
  box.appendChild(foldedTurn(2, { tok: 700 }));
  ctx.api.wbFoldSync();
  assert.equal(headers(box)[0].wbTok.textContent, "~+1.2k tok");
});

test("a standalone system mail row between folded turns joins the cluster", () => {
  const { ctx, box } = load({ stored: "on" });
  const a = box.appendChild(foldedTurn(1, { mails: 1 }));
  const m = box.appendChild(sysMail(2));
  const b = box.appendChild(foldedTurn(3, { mails: 1 }));
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 1);
  const h = headers(box)[0];
  assert.equal(h.wbCount.textContent, "js.conv.cluster.turns.other:2", "only turns are counted as turns");
  assert.equal(h.wbMails.textContent, "js.conv.cluster.mails.other:3", "the system mail counts as a mail");
  assert.deepEqual([a, m, b].map(state), ["clustered", "clustered", "clustered"]);
  assert.equal(h.wbTime.textContent, hm(T0 + MIN) + ".." + hm(T0 + 3 * MIN));
});

test("a run of only system mails is labelled by its mails", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(sysMail(1));
  box.appendChild(sysMail(2));
  ctx.api.wbFoldSync();
  const h = headers(box)[0];
  assert.equal(h.wbCount.textContent, "js.conv.cluster.mails.other:2");
  assert.equal(h.wbMails.hasAttribute("hidden"), true);
});

test("a board-nudge wake row counts as a system row; a plain mail wake does not", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(foldedTurn(1));
  const nudge = box.appendChild(new El("details", { class: "mail-notification turn-trigger", "data-entry": "w2", "data-board-nudge": "1", "data-mail-ts": String(T0 + 2 * MIN) }));
  box.appendChild(foldedTurn(3));
  const unmatched = box.appendChild(new El("details", { class: "mail-notification turn-trigger", "data-entry": "w4" }));
  box.appendChild(foldedTurn(5));
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 1);
  assert.equal(state(nudge), "clustered");
  assert.equal(state(unmatched), "plain", "an unmatched wake row is a boundary");
});

test("plain folded turns cluster through their in-place host and keep their replies hidden", () => {
  const { ctx, box } = load({ stored: "on" });
  const p1 = plainTurn(1), p2 = plainTurn(2);
  box.appendChild(p1.reply); box.appendChild(p1.host);
  box.appendChild(p2.reply); box.appendChild(p2.host);
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 1, "the hidden replies between the hosts do not break the run");
  assert.equal(state(p1.host), "clustered");
  assert.equal(state(p2.host), "clustered");
  assert.equal(p1.host.parentNode, box);
  assert.equal(headers(box)[0].wbTok.textContent, "+200 tok");
  assert.equal(p1.reply.hasAttribute("data-wb-folded"), true);
});

test("hidden and absorbed siblings never break a run", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(foldedTurn(1, { replies: "x4" }));
  box.appendChild(new El("span", { hidden: "", "data-entry": "x1", "data-turn-absorbed": "1" }));
  box.appendChild(new El("span", { hidden: "", "data-entry": "x2", "data-turn-live-absorbed": "1" }));
  box.appendChild(new El("span", { hidden: "", "data-entry": "x3", "data-mail-wake": "suppressed" }));
  const folded = box.appendChild(reply("x4", "folded reply"));
  box.appendChild(foldedTurn(2));
  ctx.api.wbFoldSync();
  assert.equal(folded.hasAttribute("data-wb-folded"), true, "the claimed reply is folded away");
  assert.equal(headers(box).length, 1);
  assert.equal(headers(box)[0].wbCount.textContent, "js.conv.cluster.turns.other:2");
});

const breakers = {
  "user message": () => new El("div", { class: "msg user", "data-entry": "b" }),
  "user batch": () => new El("div", { class: "user-batch", "data-entry": "b" }),
  "whiteboard update": () => new El("article", { class: "wbeffects wbupdate", "data-entry": "b" }, [new El("section", { class: "wbupdate-topic" })]),
  "approval card": () => new El("div", { class: "permwait", "data-entry": "b" }),
  "live turn": () => foldedTurn(90, { live: true }),
  "failed turn": () => foldedTurn(91, { failed: 2 }),
  "compact boundary": () => new El("details", { class: "sysline boundary-badge compaction", "data-entry": "b" }),
  "notice boundary": () => new El("details", { class: "sysline boundary-badge notice fail", "data-entry": "b" }),
  "command": () => new El("div", { class: "cmdrun", "data-entry": "b" }),
  "peer mail": () => new El("article", { class: "mail-block", "data-entry": "b" }),
  "unfolded reply": () => reply("b", "visible reply"),
  "error banner": () => new El("div", { class: "errbanner" }),
};
for (const [name, make] of Object.entries(breakers)) {
  test("breaker: " + name + " splits runs and is never hidden", () => {
    const { ctx, box } = load({ stored: "on" });
    const members = [1, 2].map((n) => box.appendChild(foldedTurn(n)));
    const brk = box.appendChild(make());
    members.push(...[3, 4].map((n) => box.appendChild(foldedTurn(n))));
    ctx.api.wbFoldSync();
    assert.equal(headers(box).length, 2, "two runs of two");
    assert.equal(state(brk), "plain");
    assert.deepEqual(members.map(state), ["clustered", "clustered", "clustered", "clustered"]);
    assert.equal(box.children.indexOf(headers(box)[1]) > box.children.indexOf(brk), true);
  });
  test("breaker: " + name + " between single turns clusters nothing", () => {
    const { ctx, box } = load({ stored: "on" });
    const a = box.appendChild(foldedTurn(1));
    box.appendChild(make());
    const b = box.appendChild(foldedTurn(2));
    ctx.api.wbFoldSync();
    assert.equal(headers(box).length, 0);
    assert.deepEqual([a, b].map(state), ["plain", "plain"]);
  });
}

test("a single isolated folded turn stays a normal row", () => {
  const { ctx, box } = load({ stored: "on" });
  const a = box.appendChild(foldedTurn(1));
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 0);
  assert.equal(state(a), "plain");
  assert.equal(box.children.length, 1);
});

test("expanding and collapsing toggles members in place and keeps drawer state", () => {
  const { ctx, box, calls } = load({ stored: "on" });
  const turns = [1, 2, 3].map((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  const h = headers(box)[0];
  turns[1].open = true;
  h.click();
  assert.equal(h.getAttribute("aria-expanded"), "true");
  assert.deepEqual(turns.map(state), ["open", "open", "open"]);
  assert.deepEqual(box.children.slice(1), turns, "members stay direct children in order");
  assert.equal(turns[1].open, true, "a member's drawer stays open");
  assert.ok(calls.includes("scrollState") && calls.includes("restoreScroll"), "the toggle is scroll-locked");
  h.click();
  assert.equal(h.getAttribute("aria-expanded"), "false");
  assert.deepEqual(turns.map(state), ["clustered", "clustered", "clustered"]);
  assert.equal(turns[1].open, true, "drawer state survives a collapse");
  assert.equal(headers(box).length, 1);
  assert.equal(headers(box)[0], h, "the header node is reused");
});

test("repeated syncs and SSE ticks keep one header, leave an unchanged cluster write-free, and ignore header mutations", () => {
  const { ctx, box, rafs, observer } = load({ stored: "on" });
  const turns = [1, 2, 3].map((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  const h = headers(box)[0];
  h.click();
  const before = writes.n;
  ctx.api.wbFoldSync();
  ctx.api.wbFoldSync();
  assert.equal(writes.n, before, "an unchanged cluster performs no DOM write");
  assert.equal(headers(box).length, 1);
  assert.equal(headers(box)[0], h);
  assert.equal(h.getAttribute("aria-expanded"), "true", "open state survives the re-sync");

  // The observer ignores our own header insert/remove.
  observer.cb([{ addedNodes: [h], removedNodes: [h] }]);
  assert.equal(rafs.length, 0, "cluster header mutations never reschedule a sync");
  assert.equal(ctx.api.wbNeedsFoldForMutations([{ addedNodes: [h], removedNodes: [] }]), false);

  // A merge replaces member t2 with a refreshed node and appends a new turn.
  const fresh = foldedTurn(2, { calls: 7, tok: 4000 });
  box.children.splice(box.children.indexOf(turns[1]), 1, fresh);
  fresh.parentNode = box;
  const t4 = box.appendChild(foldedTurn(4, { ts: T0 + 30 * MIN }));
  observer.cb([{ addedNodes: [fresh, t4], removedNodes: [turns[1]] }]);
  assert.equal(rafs.length, 1, "a keyed entry mutation schedules one sync");
  rafs.shift()();
  assert.equal(headers(box).length, 1);
  assert.equal(headers(box)[0], h, "the header is patched, not recreated");
  assert.equal(h.wbCount.textContent, "js.conv.cluster.turns.other:4");
  assert.equal(h.wbCalls.textContent, "js.conv.cluster.tools.other:13", "2 + 7 (replaced) + 2 + 2 (appended)");
  assert.equal(h.wbTime.textContent, hm(T0 + MIN) + ".." + hm(T0 + 30 * MIN));
  assert.deepEqual([turns[0], fresh, turns[2], t4].map(state), ["open", "open", "open", "open"], "the replaced and the new member take the cluster's state");
});

test("a new system mail row at the end schedules a sync and joins the open cluster", () => {
  const { ctx, box, rafs, observer } = load({ stored: "on" });
  box.appendChild(foldedTurn(1));
  box.appendChild(foldedTurn(2));
  ctx.api.wbFoldSync();
  const m = box.appendChild(sysMail(3));
  observer.cb([{ addedNodes: [m], removedNodes: [] }]);
  assert.equal(rafs.length, 1);
  rafs.shift()();
  assert.equal(state(m), "clustered");
  assert.equal(headers(box).length, 1);
});

test("a live turn completing joins the previous cluster and keeps its open state", () => {
  const { ctx, box } = load({ stored: "on" });
  box.appendChild(foldedTurn(1));
  box.appendChild(foldedTurn(2));
  const live = box.appendChild(foldedTurn(3, { live: true }));
  ctx.api.wbFoldSync();
  assert.equal(state(live), "plain");
  headers(box)[0].click();
  live.attrs.class = "toolsline d-turn";
  ctx.api.wbFoldSync();
  assert.equal(state(live), "open", "the completed turn joins the opened cluster");
  assert.equal(headers(box).length, 1);
});

test("older history extending the front keeps an opened cluster open under one header", () => {
  const { ctx, box } = load({ stored: "on" });
  const a = box.appendChild(foldedTurn(5));
  const b = box.appendChild(foldedTurn(6));
  ctx.api.wbFoldSync();
  headers(box)[0].click();
  const older = foldedTurn(4);
  box.insertBefore(older, box.children[0]);
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 1);
  assert.equal(headers(box)[0].getAttribute("data-wb-cluster-key"), "t4");
  assert.equal(box.children[0], headers(box)[0], "the header moves in front of the new first member");
  assert.deepEqual([older, a, b].map(state), ["open", "open", "open"]);
});

test("eviction of the oldest rows (and the header before them) leaves a consistent box", () => {
  const { ctx, box } = load({ stored: "on" });
  const t = [1, 2, 3].map((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  // evictEntries drops the oldest entry and every non-entry node before the new oldest.
  box.children.splice(0, 2);
  t[0].parentNode = null;
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 1, "two members left: a fresh header is placed");
  assert.equal(box.children[0], headers(box)[0]);
  box.children.splice(0, 2);
  ctx.api.wbFoldSync();
  assert.equal(headers(box).length, 0, "a lone survivor dissolves the cluster");
  assert.equal(state(t[2]), "plain", "and shows as a normal row");
});

test("an entry jump target inside a collapsed cluster opens its cluster", () => {
  const { ctx, box } = load({ stored: "on" });
  const t = [1, 2, 3].map((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  const inner = t[1].children[0];
  inner.parentNode = t[1];
  ctx.api.wbRevealClustered(box, inner);
  assert.deepEqual(t.map(state), ["open", "open", "open"]);
  assert.equal(headers(box)[0].getAttribute("aria-expanded"), "true");
});

test("toggling the mode OFF removes every header and mark with no refetch, and ON restores clusters", () => {
  const { ctx, box, btn } = load({ stored: "on" });
  const t = [1, 2, 3, 4].map((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  headers(box)[0].click();
  assert.equal(headers(box).length, 1);
  btn.click();
  assert.equal(ctx.api.wbOnlyOn(), false);
  assert.equal(headers(box).length, 0);
  assert.deepEqual(t.map(state), ["plain", "plain", "plain", "plain"]);
  assert.equal(box.children.length, 4, "every original row is still there");
  btn.click();
  assert.equal(headers(box).length, 1);
  assert.deepEqual(t.map(state), ["open", "open", "open", "open"], "the reader's open choice is kept for the session");
});

test("the entries merge contract: cluster code only marks members and adds a keyless header", () => {
  const conv = fs.readFileSync(path.join(__dirname, "..", "assets", "dashboard-conversation.js"), "utf8");
  assert.ok(conv.split("\n").length < 2000, "dashboard-conversation.js stays under the 2,000-line guard");
  const { ctx, box } = load({ stored: "on" });
  [1, 2].forEach((n) => box.appendChild(foldedTurn(n)));
  ctx.api.wbFoldSync();
  assert.equal(headers(box)[0].hasAttribute("data-entry"), false, "the header carries no entry key");
});

test("the send path sets whiteboard_first only while ON", () => {
  const delivery = fs.readFileSync(path.join(__dirname, "..", "assets", "dashboard-composer-delivery.js"), "utf8");
  assert.match(delivery, /if \(typeof wbOnlyOn === "function" && wbOnlyOn\(\)\) body\.whiteboard_first = true;/);
});
